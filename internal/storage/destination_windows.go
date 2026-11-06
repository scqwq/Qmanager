package storage

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
)

// DestinationKind uses the user's actual Desktop, including redirected folders.
func DestinationKind(path string) (string, error) {
	target, err := finalDirectory(path)
	if err != nil {
		return "", err
	}
	desktop, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, windows.KF_FLAG_DONT_VERIFY)
	if err != nil {
		return "", err
	}
	if resolved, e := finalDirectory(desktop); e == nil {
		desktop = resolved
	}
	return destinationKind(target, desktop), nil
}

func destinationKind(target, desktop string) string {
	relative, err := filepath.Rel(strings.ToLower(desktop), strings.ToLower(target))
	if err == nil && (relative == "." || filepath.IsLocal(relative)) {
		return "desktop"
	}
	if strings.EqualFold(filepath.VolumeName(target), "C:") {
		return "drive_c"
	}
	return ""
}

func finalDirectory(path string) (string, error) {
	directory, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("请选择有效的保存目录")
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(directory.Fd()), &buf[0], uint32(len(buf)), 0)
	if err != nil || n >= uint32(len(buf)) {
		return "", errors.New("无法识别保存目录")
	}
	actual := windows.UTF16ToString(buf[:n])
	if strings.HasPrefix(actual, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(actual, `\\?\UNC\`), nil
	}
	return strings.TrimPrefix(actual, `\\?\`), nil
}
