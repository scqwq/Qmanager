package media

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"strings"
	"syscall"
	"time"
)

func isLink(path string, info os.FileInfo) bool {
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return true
	}
	p, err := windows.UTF16PtrFromString(NativePath(path))
	if err != nil {
		return true
	}
	attrs, err := windows.GetFileAttributes(p)
	return err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func openRead(root, relative string) (*os.File, error) {
	path, err := checkedPath(root, relative)
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(NativePath(path))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	buf := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || n >= uint32(len(buf)) {
		f.Close()
		return nil, errors.New("无法验证文件真实路径")
	}
	actual := windows.UTF16ToString(buf[:n])
	if strings.HasPrefix(actual, `\\?\UNC\`) {
		actual = `\\` + strings.TrimPrefix(actual, `\\?\UNC\`)
	} else {
		actual = strings.TrimPrefix(actual, `\\?\`)
	}
	if !strings.EqualFold(actual, path) {
		f.Close()
		return nil, errors.New("文件真实路径不匹配")
	}
	return f, nil
}

func createdAt(info os.FileInfo) *time.Time {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return nil
	}
	t := time.Unix(0, data.CreationTime.Nanoseconds()).UTC()
	return &t
}

func fileIdentity(f *os.File) string {
	var data windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &data) != nil {
		return ""
	}
	return fmt.Sprintf("%08x:%08x%08x:%d", data.VolumeSerialNumber, data.FileIndexHigh, data.FileIndexLow, data.CreationTime.Nanoseconds())
}
