package winsvc

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	modadvapi32                       = syscall.NewLazyDLL("advapi32.dll")
	procStartServiceCtrlDispatcherW   = modadvapi32.NewProc("StartServiceCtrlDispatcherW")
	procRegisterServiceCtrlHandlerExW = modadvapi32.NewProc("RegisterServiceCtrlHandlerExW")
	procSetServiceStatus              = modadvapi32.NewProc("SetServiceStatus")
)

const (
	serviceWin32OwnProcess = 0x10

	stateStopped      = 1
	stateStartPending = 2
	stateStopPending  = 3
	stateRunning      = 4

	acceptStop     = 0x1
	acceptShutdown = 0x4

	controlStop        = 1
	controlInterrogate = 4
	controlShutdown    = 5

	errorCallNotImplemented        = 120
	errorServiceSpecificError      = 1066
	errorFailedServiceControllerCx = 1063
)

type serviceStatus struct {
	ServiceType             uint32
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}

type serviceTableEntry struct {
	name *uint16
	proc uintptr
}

var (
	cbOnce sync.Once
	mainCb uintptr
	ctlCb  uintptr

	mu       sync.Mutex
	name     *uint16
	runFn    func(context.Context) error
	handle   uintptr
	status   serviceStatus
	cancelFn context.CancelFunc
	runErr   error
)

func setStatusLocked(state, accepts, exitCode uint32, waitHint uint32) {
	status.ServiceType = serviceWin32OwnProcess
	status.CurrentState = state
	status.ControlsAccepted = accepts
	status.Win32ExitCode = 0
	status.ServiceSpecificExitCode = 0
	if exitCode != 0 {
		status.Win32ExitCode = errorServiceSpecificError
		status.ServiceSpecificExitCode = exitCode
	}
	if state == stateStartPending || state == stateStopPending {
		status.CheckPoint++
	} else {
		status.CheckPoint = 0
	}
	status.WaitHint = waitHint
	if handle != 0 {
		st := status
		procSetServiceStatus.Call(handle, uintptr(unsafe.Pointer(&st)))
	}
}

func setStatus(state, accepts, exitCode, waitHint uint32) {
	mu.Lock()
	defer mu.Unlock()
	setStatusLocked(state, accepts, exitCode, waitHint)
}

// ctlHandler is the HandlerEx callback (runs on the dispatcher thread).
func ctlHandler(ctl, evtype, evdata, context uintptr) uintptr {
	switch uint32(ctl) {
	case controlStop, controlShutdown:
		mu.Lock()
		setStatusLocked(stateStopPending, 0, 0, 20000)
		c := cancelFn
		mu.Unlock()
		if c != nil {
			c()
		}
		return 0
	case controlInterrogate:
		mu.Lock()
		if handle != 0 {
			st := status
			procSetServiceStatus.Call(handle, uintptr(unsafe.Pointer(&st)))
		}
		mu.Unlock()
		return 0
	}
	return errorCallNotImplemented
}

// serviceMain is the ServiceMain callback (runs on a thread the SCM
// dispatcher creates); it returns once the service has stopped.
func serviceMain(argc, argv uintptr) uintptr {
	h, _, err := procRegisterServiceCtrlHandlerExW.Call(uintptr(unsafe.Pointer(name)), ctlCb, 0)
	if h == 0 {
		mu.Lock()
		runErr = errors.New("RegisterServiceCtrlHandlerExW: " + err.Error())
		mu.Unlock()
		return 0
	}
	ctx, cancel := context.WithCancel(context.Background())
	mu.Lock()
	handle = h
	cancelFn = cancel
	setStatusLocked(stateStartPending, 0, 0, 10000)
	mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- runFn(ctx) }()
	setStatus(stateRunning, acceptStop|acceptShutdown, 0, 0)
	err2 := <-done
	cancel()
	var code uint32
	mu.Lock()
	if err2 != nil {
		runErr = err2
		code = 1
	}
	setStatusLocked(stateStopped, 0, code, 0)
	mu.Unlock()
	return 0
}

// Run connects to the SCM and runs fn as service svcName until the SCM stops
// it (fn's ctx is then cancelled) or fn returns. It must be called from the
// process the SCM started; from a console it fails with
// ERROR_FAILED_SERVICE_CONTROLLER_CONNECT.
func Run(svcName string, fn func(ctx context.Context) error) error {
	if err := modadvapi32.Load(); err != nil {
		return err
	}
	p, err := syscall.UTF16PtrFromString(svcName)
	if err != nil {
		return err
	}
	cbOnce.Do(func() {
		mainCb = syscall.NewCallback(serviceMain)
		ctlCb = syscall.NewCallback(ctlHandler)
	})
	mu.Lock()
	name, runFn = p, fn
	mu.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	table := []serviceTableEntry{{name: p, proc: mainCb}, {}}
	r, _, callErr := procStartServiceCtrlDispatcherW.Call(uintptr(unsafe.Pointer(&table[0])))
	runtime.KeepAlive(table)
	if r == 0 {
		if en, ok := callErr.(syscall.Errno); ok && en == errorFailedServiceControllerCx {
			return errors.New("--service: not started by the Windows Service Control Manager (use `imonitord run` from a console)")
		}
		return errors.New("StartServiceCtrlDispatcherW: " + callErr.Error())
	}
	mu.Lock()
	defer mu.Unlock()
	return runErr
}
