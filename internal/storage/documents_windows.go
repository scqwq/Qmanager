package storage

import (
	"golang.org/x/sys/windows"
	"runtime"
	"syscall"
)

// Ask Windows for the current user's redirected Documents path, without
// requesting creation, verification, elevation or a search of other profiles.
func DocumentsDirectory() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err != nil && err != syscall.Errno(1) {
		return "", err
	}
	defer windows.CoUninitialize()
	return windows.KnownFolderPath(windows.FOLDERID_Documents, windows.KF_FLAG_DONT_VERIFY)
}
