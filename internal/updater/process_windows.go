//go:build windows

package updater

import (
	"time"

	"golang.org/x/sys/windows"
)

func waitProcessExit(pid int, timeout time.Duration) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return true
	}
	defer windows.CloseHandle(handle)
	milliseconds := uint32(timeout / time.Millisecond)
	result, err := windows.WaitForSingleObject(handle, milliseconds)
	return err == nil && result == windows.WAIT_OBJECT_0
}
