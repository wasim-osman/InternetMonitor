// Command imonitord is the InternetMonitor agent.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"imonitord/internal/api"
	"imonitord/internal/install"
	"imonitord/internal/probe"
	"imonitord/internal/winsvc"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "1.0.0"

func usage() {
	admin := "sudo "
	if runtime.GOOS == "windows" {
		admin = "(elevated) "
	}
	fmt.Fprintf(os.Stderr, `imonitord %s - InternetMonitor agent (%s/%s)

Usage:
  imonitord [run] [--listen :7878] [--state-dir DIR] [--log-file FILE]
  imonitord pair-code [--json] [--listen :7878]
  imonitord version
  %simonitord install [--listen :7878]%s
  %simonitord uninstall [--purge]

Default state dir: %s
  (falls back to %s when that is not writable)
`, version, runtime.GOOS, runtime.GOARCH, admin, taskHelp(), admin,
		install.SystemStateDir(), install.UserStateDir())
}

func taskHelp() string {
	if runtime.GOOS == "windows" {
		return " [--task]"
	}
	return ""
}

func main() {
	log.SetFlags(log.LstdFlags)
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = cmdRun(args)
	case "pair-code":
		err = cmdPairCode(args)
	case "version", "--version", "-version":
		fmt.Println(version)
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		listen := fs.String("listen", "", "listen address for the service (default :7878 or the installed value)")
		task := fs.Bool("task", false, "Windows: use a SYSTEM scheduled task instead of a Windows service")
		fs.Parse(args)
		err = install.InstallMode(*listen, *task)
	case "uninstall":
		fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
		purge := fs.Bool("purge", false, "also delete the state directory")
		fs.Parse(args)
		err = install.Uninstall(*purge)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "imonitord:", err)
		os.Exit(1)
	}
}

// runOpts are the parsed `run` flags.
type runOpts struct {
	Listen      string
	StateDir    string // "" = choose the default
	LogFile     string
	Service     bool // started by the Windows SCM
	SelfRestart bool // POST /v1/restart re-initialises in-process instead of exiting
}

// parseRunArgs parses `run` flags for goos. --service is Windows-only and
// implies --self-restart and a log file in the state directory (a service
// has no console).
func parseRunArgs(args []string, goos string) (runOpts, error) {
	var o runOpts
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.Listen, "listen", ":7878", "HTTP listen address")
	fs.StringVar(&o.StateDir, "state-dir", "", "state directory")
	fs.StringVar(&o.LogFile, "log-file", "", "append logs to this file instead of stderr")
	fs.BoolVar(&o.Service, "service", false, "run under the Windows Service Control Manager")
	fs.BoolVar(&o.SelfRestart, "self-restart", false, "restart in-process on POST /v1/restart")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if _, _, err := net.SplitHostPort(o.Listen); err != nil {
		return o, fmt.Errorf("bad --listen %q", o.Listen)
	}
	if o.Service {
		if goos != "windows" {
			return o, errors.New("--service is only supported on Windows")
		}
		o.SelfRestart = true
		if o.LogFile == "" && o.StateDir != "" {
			o.LogFile = filepath.Join(o.StateDir, "imonitord.log")
		}
	}
	return o, nil
}

const maxLogBytes = 10 << 20

// openLog opens path for appending, rotating it to path.1 when it is large.
func openLog(path string) (*os.File, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		os.Remove(path + ".1")
		os.Rename(path, path+".1")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

func cmdRun(args []string) error {
	o, err := parseRunArgs(args, runtime.GOOS)
	if err != nil {
		usage()
		return err
	}
	if o.StateDir == "" {
		o.StateDir = install.DefaultStateDir()
		if o.StateDir != install.SystemStateDir() {
			log.Printf("%s is not writable (not root/Administrator?); using %s", install.SystemStateDir(), o.StateDir)
		}
		if o.Service && o.LogFile == "" {
			o.LogFile = filepath.Join(o.StateDir, "imonitord.log")
		}
	}
	if o.LogFile != "" {
		f, err := openLog(o.LogFile)
		if err != nil {
			return fmt.Errorf("log file: %w", err)
		}
		defer f.Close()
		log.SetOutput(f)
	}

	// Keep the heap small: soft memory limit + more frequent GC.
	debug.SetMemoryLimit(16 << 20)
	debug.SetGCPercent(50)
	probe.Version = version

	if o.Service {
		log.Printf("imonitord %s starting as a Windows service", version)
		err := winsvc.Run(install.ServiceName, func(ctx context.Context) error {
			return serve(ctx, o)
		})
		if err != nil {
			log.Printf("service: %v", err)
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, o)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// serve runs the agent until ctx is done. With SelfRestart (Windows service /
// scheduled task, where nothing relaunches an exited process reliably),
// POST /v1/restart rebuilds the app in-process, and start-up errors are
// retried instead of ending the process.
func serve(ctx context.Context, o runOpts) error {
	for {
		app, err := api.NewApp(api.Options{Version: version, StateDir: o.StateDir})
		if err != nil {
			if !o.SelfRestart {
				return err
			}
			log.Printf("start failed: %v; retrying in 5 s", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return nil
			}
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		restarted := make(chan struct{})
		if o.SelfRestart {
			var once sync.Once
			app.Exit = func(int) {
				once.Do(func() { close(restarted) })
				cancel()
			}
		}
		err = app.Serve(runCtx, o.Listen)
		app.Shutdown()
		cancel()
		if ctx.Err() != nil {
			log.Printf("stopped")
			return nil
		}
		select {
		case <-restarted:
			log.Printf("restarting in-process")
			continue
		default:
		}
		if err != nil && o.SelfRestart {
			log.Printf("serve: %v; retrying in 5 s", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return nil
			}
			continue
		}
		if err == nil {
			log.Printf("stopped")
		}
		return err
	}
}

func cmdPairCode(args []string) error {
	fs := flag.NewFlagSet("pair-code", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	listen := fs.String("listen", "", "agent listen address (default: installed unit, $IMONITOR_LISTEN or :7878)")
	fs.Parse(args)
	addr := *listen
	if addr == "" {
		addr = os.Getenv("IMONITOR_LISTEN")
	}
	if addr == "" {
		addr = install.InstalledListen()
	}
	if addr == "" {
		addr = ":7878"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bad listen address %q", addr)
	}
	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/v1/local/pair-code"
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("cannot reach the agent on 127.0.0.1:%s (is imonitord running?): %v", port, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if *asJSON {
		os.Stdout.Write(body)
		return nil
	}
	var r struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return err
	}
	fmt.Printf("Pairing code: %s (expires %s)\n", r.Code, time.Unix(r.ExpiresAt, 0).Format("15:04:05"))
	return nil
}
