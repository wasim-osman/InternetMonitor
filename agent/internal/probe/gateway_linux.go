package probe

import (
	"bufio"
	"net"
	"os"
	"time"
)

const gwCacheTTL = time.Duration(0)

func defaultRoute() (net.IP, string, error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	return ParseProcNetRoute(bufio.NewScanner(f))
}
