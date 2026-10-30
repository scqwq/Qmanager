package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func NativePath(path string) string {
	if runtime.GOOS != "windows" || strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}

func OpenRelative(root, relative string) (*os.File, error) { return openRead(root, relative) }
func OpenItem(item Item) (*os.File, error) {
	f, err := openRead(item.Root, item.Path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != item.Bytes || !info.ModTime().Equal(readTime(item)) || readIdentity(item) != "" && fileIdentity(f) != readIdentity(item) {
		f.Close()
		return nil, errors.New("源文件已变化，请重新扫描")
	}
	return f, nil
}
func readTime(item Item) time.Time {
	if item.PhysicalModifiedAt != nil {
		return *item.PhysicalModifiedAt
	}
	return item.ModifiedAt
}
func readIdentity(item Item) string {
	if item.PhysicalIdentity != "" {
		return item.PhysicalIdentity
	}
	return item.Identity
}
func SameItem(f *os.File, item Item) bool {
	info, err := f.Stat()
	return err == nil && info.Size() == item.Bytes && info.ModTime().Equal(readTime(item)) && (readIdentity(item) == "" || fileIdentity(f) == readIdentity(item))
}
func AttachObject(item Item, root, relative string) (Item, error) {
	f, err := openRead(root, relative)
	if err != nil {
		return item, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != item.Bytes {
		return item, errors.New("对象长度不匹配")
	}
	item.Root = root
	item.Path = relative
	item.InfoIdentity = info
	t := info.ModTime().UTC()
	item.PhysicalModifiedAt = &t
	item.PhysicalIdentity = fileIdentity(f)
	item.Format, item.Status, item.Preview = detect(f, info.Size())
	return item, nil
}
func Canonical(path string) (string, error) {
	a, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for p := a; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil {
			if isLink(p, info) {
				return "", errors.New("输出或索引路径不能包含链接")
			}
		} else if !os.IsNotExist(e) {
			return "", e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return a, nil
}
func Overlap(a, b string) bool {
	a, b = strings.ToLower(filepath.Clean(a)), strings.ToLower(filepath.Clean(b))
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, e := filepath.Rel(pair[0], pair[1])
		if e == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}

type CancelReader struct {
	Ctx    context.Context
	Reader io.Reader
}

func (r CancelReader) Read(p []byte) (int, error) {
	if err := r.Ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
