package storage

import (
	"errors"
	"golang.org/x/sys/windows"
	"strings"
	"unsafe"
)

// QQRunning only enumerates process names. It never opens, modifies or stops QQ.
func QQRunning() (bool, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(snapshot, &entry)
	for err == nil {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "QQ.exe") {
			return true, nil
		}
		err = windows.Process32Next(snapshot, &entry)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return false, nil
	}
	return false, err
}
