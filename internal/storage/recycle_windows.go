package storage

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	recycleOLE         = windows.NewLazySystemDLL("ole32.dll")
	recycleShell       = windows.NewLazySystemDLL("shell32.dll")
	coInitialize       = recycleOLE.NewProc("CoInitializeEx")
	coUninitialize     = recycleOLE.NewProc("CoUninitialize")
	coCreate           = recycleOLE.NewProc("CoCreateInstance")
	createShellItem    = recycleShell.NewProc("SHCreateItemFromParsingName")
	clsidFileOperation = recycleGUID("{3AD05575-8857-4850-9277-11B85BDB8E09}")
	iidFileOperation   = recycleGUID("{947AAB5F-0A5C-4C13-B4D6-4BF7836FC9F8}")
	iidShellItem       = recycleGUID("{43826D1E-E718-42EE-BC55-A1E261C37BFE}")
	iidProgressSink    = recycleGUID("{04B0F1A7-9490-44BC-96E1-4296A31252E2}")
	iidUnknown         = recycleGUID("{00000000-0000-0000-C000-000000000046}")
)

func recycleGUID(value string) windows.GUID {
	g, err := windows.GUIDFromString(value)
	if err != nil {
		panic(err)
	}
	return g
}

type shellObject struct{ vtable *[23]uintptr }

func (o *shellObject) call(slot int, args ...uintptr) uintptr {
	params := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	hr, _, _ := syscall.SyscallN(o.vtable[slot], params...)
	runtime.KeepAlive(o)
	return hr
}
func hrError(hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("Windows 回收操作失败（0x%08X）", uint32(hr))
	}
	return nil
}

const abortHRESULT = uintptr(0x80004004)

type recycleSink struct {
	vtable   *[19]uintptr
	ctx      context.Context
	check    func() error
	checked  bool
	recycled bool
	failure  error
	location string
}

// Static callbacks avoid allocating/leaking a new Windows callback per file.
var recycleVtable = [19]uintptr{
	windows.NewCallback(func(s *recycleSink, iid *windows.GUID, out *uintptr) uintptr {
		if out == nil || iid == nil {
			return uintptr(0x80004003)
		}
		*out = 0
		if *iid != iidUnknown && *iid != iidProgressSink {
			return uintptr(0x80004002)
		}
		*out = uintptr(unsafe.Pointer(s))
		return 0
	}),
	windows.NewCallback(func(s *recycleSink) uintptr { return 2 }),
	windows.NewCallback(func(s *recycleSink) uintptr { return 1 }),
	windows.NewCallback(func(s *recycleSink) uintptr {
		if s.ctx.Err() != nil {
			return abortHRESULT
		}
		return 0
	}),
	windows.NewCallback(func(s *recycleSink, hr uintptr) uintptr { return 0 }),
	windows.NewCallback(func(s *recycleSink, flags, item, name uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, item, name, hr, newItem uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, item, dest, name uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, item, dest, name, hr, newItem uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, item, dest, name uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, item, dest, name, hr, newItem uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, item uintptr) uintptr {
		if s.ctx.Err() != nil || s.checked {
			return abortHRESULT
		}
		if flags&0x80 == 0 {
			s.failure = errors.New("Windows 未选择回收模式，已拒绝操作")
			return abortHRESULT
		}
		s.failure = s.check()
		if s.failure != nil {
			return abortHRESULT
		}
		s.checked = true
		return 0
	}),
	windows.NewCallback(func(s *recycleSink, flags, item, hr uintptr, newItem *shellObject) uintptr {
		if e := hrError(hr); e != nil {
			s.failure = e
			return abortHRESULT
		}
		if !s.checked || newItem == nil {
			s.failure = errors.New("Windows 未确认回收站条目，请核对文件状态")
			return abortHRESULT
		}
		s.recycled = true
		var p *uint16
		if hrError(newItem.call(5, 0x80058000, uintptr(unsafe.Pointer(&p)))) == nil && p != nil {
			s.location = windows.UTF16PtrToString(p)
			recycleOLE.NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(p)))
		}
		return 0
	}),
	windows.NewCallback(func(s *recycleSink, flags, dest, name uintptr) uintptr { return abortHRESULT }),
	windows.NewCallback(func(s *recycleSink, flags, dest, name, template, attributes, hr, newItem uintptr) uintptr {
		return abortHRESULT
	}),
	windows.NewCallback(func(s *recycleSink, total, done uintptr) uintptr {
		if s.ctx.Err() != nil {
			return abortHRESULT
		}
		return 0
	}),
	windows.NewCallback(func(s *recycleSink) uintptr { return 0 }),
	windows.NewCallback(func(s *recycleSink) uintptr { return 0 }),
	windows.NewCallback(func(s *recycleSink) uintptr { return 0 }),
}

func RecycleFile(ctx context.Context, path string, check func() error) error {
	_, err := recycleNative(ctx, path, check)
	return err
}

// Only IFileOperation with mandatory recycling is used, never DeleteFile or a
// permanent fallback. Success requires a non-null per-item Recycle Bin object.
func recycleNative(ctx context.Context, path string, check func() error) (string, error) {
	if check == nil {
		return "", errors.New("缺少文件核对回调")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitialize.Call(0, 2)
	if err := hrError(hr); err != nil {
		return "", err
	}
	defer coUninitialize.Call()
	var operation *shellObject
	hr, _, _ = coCreate.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, 1, uintptr(unsafe.Pointer(&iidFileOperation)), uintptr(unsafe.Pointer(&operation)))
	if err := hrError(hr); err != nil {
		return "", err
	}
	if operation == nil {
		return "", errors.New("无法创建 Windows 回收服务")
	}
	var sink *recycleSink
	defer func() { operation.call(2); runtime.KeepAlive(sink) }()
	const flags = 0x00080000 | 0x20000000 | 0x00100000 | 0x00800000 | 0x2000 | 0x1000 | 0x0400 | 0x0040 | 0x0010 | 0x0004 | 0x4000
	if err := hrError(operation.call(5, flags)); err != nil {
		return "", err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	var item *shellObject
	hr, _, _ = createShellItem.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&item)))
	if err = hrError(hr); err != nil {
		return "", err
	}
	if item == nil {
		return "", errors.New("无法定位待回收文件")
	}
	defer item.call(2)
	sink = &recycleSink{vtable: &recycleVtable, ctx: ctx, check: check}
	if err = hrError(operation.call(18, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(sink)))); err != nil {
		return "", err
	}
	perform := hrError(operation.call(21))
	var aborted int32
	abortedErr := hrError(operation.call(22, uintptr(unsafe.Pointer(&aborted))))
	runtime.KeepAlive(sink)
	runtime.KeepAlive(p)
	if sink.recycled {
		return sink.location, nil
	}
	if sink.failure != nil {
		return "", sink.failure
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if perform != nil {
		return "", perform
	}
	if abortedErr != nil {
		return "", abortedErr
	}
	if aborted != 0 {
		return "", errors.New("Windows 回收操作已取消")
	}
	return "", errors.New("未收到逐项回收成功确认，文件状态待核对")
}
