//go:build !linux && !darwin && !windows

package probe

import (
	"errors"
	"net"
	"time"
)

const gwCacheTTL = 5 * time.Second

func defaultRoute() (net.IP, string, error) { return nil, "", errors.New("unsupported OS") }
