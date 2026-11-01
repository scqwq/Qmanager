package media

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
)

// RecycleGuard pins directory ancestors and denies file writes while the
// Shell's PreDeleteItem callback rechecks the exact file instance. Delete
// sharing is required because the Shell binds its target before that callback.
type RecycleGuard struct {
	File    *os.File
	parents []windows.Handle
}

func (g *RecycleGuard) ReleaseFile() {
	if g.File != nil {
		g.File.Close()
		g.File = nil
	}
}
func (g *RecycleGuard) Close() {
	g.ReleaseFile()
	for i := len(g.parents) - 1; i >= 0; i-- {
		windows.CloseHandle(g.parents[i])
	}
	g.parents = nil
}
func LockForRecycle(item Item) (*RecycleGuard, error) {
	path, err := checkedPath(item.Root, item.Path)
	if err != nil {
		return nil, err
	}
	if filepath.VolumeName(path) == "" || strings.HasPrefix(path, `\\`) {
		return nil, errors.New("仅支持本机磁盘媒体回收")
	}
	g := &RecycleGuard{}
	ok := false
	defer func() {
		if !ok {
			g.Close()
		}
	}()
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		p, e := windows.UTF16PtrFromString(NativePath(dir))
		if e != nil {
			return nil, e
		}
		h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			return nil, e
		}
		g.parents = append(g.parents, h)
		var info windows.ByHandleFileInformation
		if e = windows.GetFileInformationByHandle(h, &info); e != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, errors.New("目标祖先路径含链接或无法核对")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	p, err := windows.UTF16PtrFromString(NativePath(path))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	g.File = os.NewFile(uintptr(h), path)
	if !SameItem(g.File, item) || fileIdentity(g.File) == "" {
		return nil, errors.New("文件实例已变化或无法核对")
	}
	// Re-open through the normal verified path and compare against the pinned
	// handle, so a root/path change cannot silently authorize a different file.
	verified, err := OpenItem(item)
	if err != nil {
		return nil, err
	}
	a, e1 := verified.Stat()
	b, e2 := g.File.Stat()
	verified.Close()
	if e1 != nil || e2 != nil || !os.SameFile(a, b) {
		return nil, errors.New("文件路径与实例不符")
	}
	ok = true
	return g, nil
}
