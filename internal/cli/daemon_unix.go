//go:build !windows

package cli

import "syscall"

// daemonSysProcAttr returns process attributes to detach the daemon.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
