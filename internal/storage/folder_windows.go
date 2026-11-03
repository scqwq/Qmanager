package storage

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"runtime"
	"syscall"
)

// OpenFolder invokes the folder-only Shell verb without a command interpreter or
// parameters. A ZIP or executable can never be passed as the Shell target.
func OpenFolder(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("打开文件夹需要完整目录路径")
	}
	path, err := media.Canonical(path)
	if err != nil {
		return errors.New("无法安全打开该文件夹")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return errors.New("文件夹不存在或无法访问")
	}
	verb, _ := windows.UTF16PtrFromString("explore")
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errors.New("文件夹路径无效")
	}
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
		// S_FALSE means this thread was already initialized in the same mode.
		if err != nil && err != syscall.Errno(1) {
			result <- errors.New("无法初始化文件资源管理器，请稍后重试")
			return
		}
		defer windows.CoUninitialize()
		if err = windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
			err = errors.New("无法打开文件资源管理器，请确认该位置可访问后重试")
		}
		result <- err
	}()
	return <-result
}
