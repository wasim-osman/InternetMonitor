package probe

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

const gwCacheTTL = 5 * time.Second

// mibIPForwardRow mirrors MIB_IPFORWARDROW (14 DWORDs).
type mibIPForwardRow struct {
	Dest, Mask, Policy, NextHop, IfIndex, Type, Proto, Age, NextHopAS uint32
	Metric1, Metric2, Metric3, Metric4, Metric5                       uint32
}

// defaultRoute asks the IP stack for the route it would use to reach a public
// address (GetBestRoute sends nothing). Falls back to parsing `route print`.
func defaultRoute() (net.IP, string, error) {
	if gw, iface, err := bestRoute(); err == nil {
		return gw, iface, nil
	}
	return routePrint()
}

func bestRoute() (net.IP, string, error) {
	if err := modiphlpapi.Load(); err != nil {
		return nil, "", err
	}
	var row mibIPForwardRow
	dst := uint32(0x01010101) // 1.1.1.1 (same in either byte order)
	r, _, _ := procGetBestRoute.Call(uintptr(dst), 0, uintptr(unsafe.Pointer(&row)))
	if r != 0 {
		return nil, "", errors.New("no default route")
	}
	if row.NextHop == 0 || row.NextHop == dst {
		return nil, ifaceName(int(row.IfIndex)), errors.New("no default gateway")
	}
	return ipFromAddr(row.NextHop), ifaceName(int(row.IfIndex)), nil
}

func ifaceName(index int) string {
	if ifc, err := net.InterfaceByIndex(index); err == nil {
		return ifc.Name
	}
	return ""
}

func routePrint() (net.IP, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	bin := "route.exe"
	if root, ok := syscall.Getenv("SystemRoot"); ok && root != "" {
		bin = filepath.Join(root, "System32", "route.exe")
	}
	cmd := exec.CommandContext(ctx, bin, "print", "-4", "0.0.0.0")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, "", errors.New("no default route")
	}
	gw, ifa, err := ParseRoutePrint(string(out))
	if err != nil {
		return nil, "", err
	}
	return gw, ifaceNameByAddr(ifa), nil
}

func ifaceNameByAddr(ip net.IP) string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifs {
		as, _ := ifc.Addrs()
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(ip) {
				return ifc.Name
			}
		}
	}
	return ""
}

// hideWindow stops console-less parents (services) from flashing a console
// window for child processes.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// HideWindow is hideWindow for other packages.
func HideWindow(cmd *exec.Cmd) { hideWindow(cmd) }
