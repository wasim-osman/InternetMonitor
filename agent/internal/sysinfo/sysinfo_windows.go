package sysinfo

import (
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var (
	modkernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64       = modkernel32.NewProc("GetTickCount64")
	procGetDiskFreeSpaceExW  = modkernel32.NewProc("GetDiskFreeSpaceExW")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func collectOS(s *System) {
	s.Kernel = strPtr(windowsVersion())
	s.Model = strPtr(hardwareModel())
	if procGetTickCount64.Find() == nil {
		// GetTickCount64 returns a 64-bit value; on 386 the high half is in r2.
		r1, r2, _ := procGetTickCount64.Call()
		ms := uint64(r1)
		if unsafe.Sizeof(uintptr(0)) == 4 {
			ms = uint64(uint32(r1)) | uint64(uint32(r2))<<32
		}
		s.UptimeSec = i64(int64(ms / 1000))
	}
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if procGlobalMemoryStatusEx.Find() == nil {
		if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); r != 0 && m.TotalPhys > 0 {
			s.MemTotalMB = i64(int64(m.TotalPhys / (1024 * 1024)))
			s.MemUsedMB = i64(int64((m.TotalPhys - m.AvailPhys) / (1024 * 1024)))
		}
	}
	// No load average, temperature or Wi-Fi details on Windows (left null/[]).
}

const hklm = syscall.Handle(0x80000002) // HKEY_LOCAL_MACHINE

// regString reads a REG_SZ (or REG_DWORD, formatted) value under HKLM.
func regString(path, name string) string {
	var k syscall.Handle
	p, _ := syscall.UTF16PtrFromString(path)
	if syscall.RegOpenKeyEx(hklm, p, 0, syscall.KEY_READ, &k) != nil {
		return ""
	}
	defer syscall.RegCloseKey(k)
	n, _ := syscall.UTF16PtrFromString(name)
	var typ, size uint32
	if syscall.RegQueryValueEx(k, n, nil, &typ, nil, &size) != nil || size == 0 || size > 4096 {
		return ""
	}
	buf := make([]byte, size)
	if syscall.RegQueryValueEx(k, n, nil, &typ, &buf[0], &size) != nil {
		return ""
	}
	switch typ {
	case syscall.REG_SZ, syscall.REG_EXPAND_SZ:
		u := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[0])), size/2)
		return strings.TrimSpace(syscall.UTF16ToString(u))
	case syscall.REG_DWORD:
		if size >= 4 {
			v := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
			return strconv.FormatUint(uint64(v), 10)
		}
	}
	return ""
}

func windowsVersion() string {
	const cv = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	return FormatWindowsVersion(regString(cv, "ProductName"), regString(cv, "DisplayVersion"),
		regString(cv, "CurrentMajorVersionNumber"), regString(cv, "CurrentMinorVersionNumber"),
		regString(cv, "CurrentBuildNumber"), regString(cv, "UBR"))
}

func hardwareModel() string {
	const bios = `HARDWARE\DESCRIPTION\System\BIOS`
	mfr, prod := regString(bios, "SystemManufacturer"), regString(bios, "SystemProductName")
	if prod == "" || strings.EqualFold(prod, "System Product Name") {
		return mfr
	}
	if mfr == "" || strings.HasPrefix(strings.ToLower(prod), strings.ToLower(mfr)) {
		return prod
	}
	return mfr + " " + prod
}

func diskFreeMB(dir string) (int64, bool) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, false
	}
	var avail, total, free uint64
	r, _, _ := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if r == 0 {
		return 0, false
	}
	return int64(avail / (1024 * 1024)), true
}

// Windows adapter names are user-facing ("Ethernet", "Wi-Fi", "WLAN").
func isPhysical(string) bool { return false }
func isWireless(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "wi-fi") || strings.HasPrefix(n, "wifi") || strings.HasPrefix(n, "wlan") ||
		strings.Contains(n, "wireless")
}
func ifaceCounters(string) (*uint64, *uint64) { return nil, nil }
