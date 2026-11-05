package cloud123

import (
	"errors"
	"golang.org/x/sys/windows"
	"qqarchive/internal/media"
	"runtime"
	"unsafe"
)

func protect(data []byte) ([]byte, error)   { return crypt(data, true) }
func unprotect(data []byte) ([]byte, error) { return crypt(data, false) }
func crypt(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 65536 {
		return nil, errors.New("凭据数据大小无效")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	entropy := []byte("Qman/123-web-session/v1")
	extra := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var e error
	if encrypt {
		e = windows.CryptProtectData(&in, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		e = windows.CryptUnprotectData(&in, nil, &extra, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if e != nil {
		return nil, errors.New("Windows无法解密或保存此用户的云盘凭据，请重新登录")
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	if out.Size == 0 || out.Size > 65536 {
		return nil, errors.New("凭据解密结果无效")
	}
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
func replaceCredential(from, to string) error {
	a, e := windows.UTF16PtrFromString(media.NativePath(from))
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(media.NativePath(to))
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
