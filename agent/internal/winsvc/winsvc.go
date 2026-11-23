// Package winsvc lets imonitord run as a real Windows service: it performs the
// Service Control Manager handshake (StartServiceCtrlDispatcherW,
// RegisterServiceCtrlHandlerExW, SetServiceStatus) with plain syscall calls,
// so no golang.org/x/sys dependency is needed. On other OSes Run fails.
package winsvc
