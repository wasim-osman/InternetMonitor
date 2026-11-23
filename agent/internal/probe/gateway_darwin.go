package probe

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"strings"
	"time"
)

const gwCacheTTL = 5 * time.Second

func defaultRoute() (net.IP, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/sbin/route", "-n", "get", "default").Output()
	if err != nil {
		return nil, "", errors.New("no default route")
	}
	var gw net.IP
	iface := ""
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "gateway":
			gw = net.ParseIP(v)
		case "interface":
			iface = v
		}
	}
	if gw == nil {
		return nil, iface, errors.New("no default gateway")
	}
	return gw, iface, nil
}
