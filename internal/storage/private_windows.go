package storage

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"qqarchive/internal/media"
	"runtime"
	"strings"
	"unsafe"
)

// Private grants only the current Windows user and SYSTEM access. Children
// inherit the restricted DACL. It never changes permissions on source paths.
func Private(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sddl := fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;SY)", user.User.Sid.String())
	// A restricted Windows test host also checks its exact package/capability
	// identities. Never grant the broad ALL APPLICATION PACKAGES groups.
	for _, class := range []uint32{31, 30} {
		var n uint32
		err = windows.GetTokenInformation(windows.GetCurrentProcessToken(), class, nil, 0, &n)
		if n == 0 || n > 65536 {
			continue
		}
		buf := make([]byte, n)
		if windows.GetTokenInformation(windows.GetCurrentProcessToken(), class, &buf[0], n, &n) != nil {
			continue
		}
		add := func(sid *windows.SID) {
			if sid == nil {
				return
			}
			value := sid.String()
			if (strings.HasPrefix(value, "S-1-15-2-") || strings.HasPrefix(value, "S-1-15-3-")) && value != "S-1-15-2-1" && value != "S-1-15-2-2" {
				sddl += "(A;OICI;FA;;;" + value + ")"
			}
		}
		if class == 31 {
			add(*(**windows.SID)(unsafe.Pointer(&buf[0])))
		} else {
			groups := (*windows.Tokengroups)(unsafe.Pointer(&buf[0]))
			for _, group := range groups.AllGroups() {
				if group.Attributes&windows.SE_GROUP_ENABLED != 0 {
					add(group.Sid)
				}
			}
		}
		runtime.KeepAlive(buf)
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(media.NativePath(path), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
func Free(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(media.NativePath(path))
	if err != nil {
		return 0, err
	}
	var free, total, all uint64
	err = windows.GetDiskFreeSpaceEx(p, &free, &total, &all)
	return free, err
}
func Publish(staged, target string) error {
	a, err := windows.UTF16PtrFromString(media.NativePath(staged))
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(media.NativePath(target))
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_WRITE_THROUGH)
}
