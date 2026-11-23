//go:build !windows

package winsvc

import (
	"context"
	"errors"
)

// Run is only meaningful on Windows.
func Run(name string, fn func(ctx context.Context) error) error {
	return errors.New("--service is only supported on Windows")
}
