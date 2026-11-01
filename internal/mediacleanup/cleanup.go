// Package mediacleanup validates a frozen media instance before recycling it.
package mediacleanup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"strings"
)

func Validate(item media.Item, source media.Source) error {
	if item.Identity == "" || item.Root != source.Resolved || !filepath.IsLocal(item.Path) {
		return errors.New("来源或文件身份无效")
	}
	matched := false
	for _, scope := range source.Scopes {
		rel, e := filepath.Rel(scope.Path, filepath.Join(item.Root, item.Path))
		if e == nil && filepath.IsLocal(rel) && scope.Kind == item.Kind {
			matched = true
		}
	}
	if !matched {
		return errors.New("文件不在受支持的媒体目录中")
	}
	if _, e := media.Canonical(filepath.Join(item.Root, item.Path)); e != nil {
		return e
	}
	if item.Stability != "stable_heuristic" || item.Role == "temporary" || item.Status != "recognized" {
		return errors.New("文件尚未稳定、为临时资源或不可识别，已跳过")
	}
	return nil
}
func hash(ctx context.Context, f *os.File, item media.Item) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(f, item.Bytes+1)}, make([]byte, 1024*1024))
	if err != nil {
		return "", err
	}
	if n != item.Bytes || !media.SameItem(f, item) {
		return "", errors.New("文件在核对期间变化")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Hash(ctx context.Context, item media.Item) (string, error) {
	f, err := media.OpenItem(item)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return hash(ctx, f, item)
}
func Recycle(ctx context.Context, item media.Item, expected string) error {
	if len(expected) != 64 || strings.Trim(expected, "0123456789abcdef") != "" {
		return errors.New("删除清单缺少有效内容摘要")
	}
	guard, err := media.LockForRecycle(item)
	if err != nil {
		return err
	}
	defer guard.Close()
	return storage.RecycleFile(ctx, filepath.Join(item.Root, item.Path), func() error {
		if guard.File == nil {
			return errors.New("文件确认回调重复，已停止")
		}
		actual, e := hash(ctx, guard.File, item)
		if e != nil {
			return e
		}
		if actual != expected {
			return errors.New("文件内容与确认清单不同，未删除")
		}
		verified, e := media.OpenItem(item)
		if e != nil {
			return e
		}
		a, e1 := verified.Stat()
		b, e2 := guard.File.Stat()
		verified.Close()
		if e1 != nil || e2 != nil || !os.SameFile(a, b) {
			return errors.New("文件在删除前被替换，已停止")
		}
		// Ancestor handles stay pinned through the operation. The guarded file is
		// released only after the Shell's immediate per-item content/path check.
		guard.ReleaseFile()
		return nil
	})
}
