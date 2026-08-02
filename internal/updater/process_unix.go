//go:build !windows

package updater

import (
	"os"
	"syscall"
	"time"
)

func waitProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		process, err := os.FindProcess(pid)
		if err != nil || process.Signal(syscall.Signal(0)) != nil {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}
