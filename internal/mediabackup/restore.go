package mediabackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"strings"
)

func restorePrefix(source string) string {
	for p := filepath.Clean(source); filepath.Dir(p) != p; p = filepath.Dir(p) {
		if strings.EqualFold(filepath.Base(p), "nt_data") && strings.EqualFold(filepath.Base(filepath.Dir(p)), "nt_qq") {
			account := filepath.Base(filepath.Dir(filepath.Dir(p)))
			suffix, e := filepath.Rel(p, source)
			if e != nil {
				return ""
			}
			prefix := account + "/nt_qq/nt_data"
			if suffix != "." {
				prefix += "/" + filepath.ToSlash(suffix)
			}
			if validPrefix(prefix) {
				return prefix
			}
			return ""
		}
	}
	return ""
}
func validPrefix(prefix string) bool {
	if !safeRelative(prefix) {
		return false
	}
	parts := strings.Split(prefix, "/")
	if len(parts) < 3 || parts[1] != "nt_qq" || parts[2] != "nt_data" {
		return false
	}
	if len(parts[0]) < 5 || len(parts[0]) > 20 {
		return false
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return false
		}
	}
	suffix := strings.Join(parts[3:], "/")
	return suffix == "" || suffix == "Pic" || suffix == "Video" || suffix == "Ptt" || suffix == "Emoji/emoji-recv"
}
func mappedPath(m Manifest, entry Entry) (string, error) {
	if !validPrefix(m.RestorePrefix) || !safeRelative(entry.Item.RelativePath) {
		return "", errors.New("此备份缺少可验证的账号路径映射，仅加载到 Qman")
	}
	path := m.RestorePrefix + "/" + entry.Item.RelativePath
	tail := strings.SplitN(path, "/nt_data/", 2)
	if len(tail) != 2 {
		return "", errors.New("恢复路径无效")
	}
	valid := false
	for prefix, kind := range map[string]string{"Pic/": "image", "Video/": "video", "Ptt/": "voice", "Emoji/emoji-recv/": "emoji"} {
		if strings.HasPrefix(tail[1], prefix) && entry.Item.Kind == kind {
			valid = true
		}
	}
	if !valid {
		return "", errors.New("恢复路径不在允许的媒体目录内")
	}
	return path, nil
}

type Placement struct {
	RelativePath string `json:"relative_path"`
	Instance     string `json:"instance"`
	Root         string `json:"root"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
}
type RestoreResult struct {
	Written    int         `json:"written"`
	Skipped    int         `json:"skipped"`
	Conflicts  int         `json:"conflicts"`
	Fallback   int         `json:"fallback"`
	Failed     int         `json:"failed"`
	Placements []Placement `json:"placements"`
}

// Root anchors writes against ancestor swaps. Explicit checks also reject
// internal links; no restore path can escape the selected root.
func checkTree(root *os.Root, path string, create bool) error {
	parts := strings.Split(filepath.ToSlash(filepath.Dir(path)), "/")
	current := ""
	for _, part := range parts {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		if create {
			if err := root.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
				return err
			}
		}
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return errors.New("目标含链接或非目录")
		}
		if _, err = media.Canonical(filepath.Join(root.Name(), current)); err != nil {
			return err
		}
	}
	return nil
}
func sameContent(ctx context.Context, root *os.Root, path, hash string, size int64) (bool, error) {
	if err := checkTree(root, path, false); err != nil {
		return false, err
	}
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("目标不是普通文件")
	}
	if _, err = media.Canonical(filepath.Join(root.Name(), path)); err != nil {
		return false, err
	}
	f, err := root.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !os.SameFile(info, before) {
		return false, errors.New("目标变化")
	}
	if before.Size() != size {
		return false, nil
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(f, size+1)}, make([]byte, 1024*1024))
	after, se := f.Stat()
	if err != nil {
		return false, err
	}
	if se != nil || n != size || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		return false, errors.New("目标在核对时变化")
	}
	return hex.EncodeToString(h.Sum(nil)) == hash, nil
}
func copyNew(ctx context.Context, item media.Item, root *os.Root, path, hash string) error {
	free, err := storage.Free(root.Name())
	if err != nil || free < uint64(item.Bytes) {
		return errors.New("目标剩余空间不足或无法查询")
	}
	if err := checkTree(root, path, true); err != nil {
		return err
	}
	in, err := media.OpenItem(item)
	if err != nil {
		return err
	}
	defer in.Close()
	stage := filepath.Join(filepath.Dir(path), ".qman-restore-"+strings.ReplaceAll(uuid.NewString(), "-", "")+".partial")
	out, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(stage)
	h := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(out, h), media.CancelReader{Ctx: ctx, Reader: io.LimitReader(in, item.Bytes+1)}, make([]byte, 1024*1024))
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || n != item.Bytes || !media.SameItem(in, item) || hex.EncodeToString(h.Sum(nil)) != hash {
		return errors.New("复制取消、失败或内容变化")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Atomic non-overwriting publication. Filesystems without hard links fall
	// back to the private Qman library instead of weakening overwrite safety.
	return root.Link(stage, path)
}
func Restore(ctx context.Context, m Manifest, items []media.Item, target, fallback string, progress func(int, int64)) (RestoreResult, error) {
	result := RestoreResult{Placements: []Placement{}}
	var dest *os.Root
	var targetErr error
	if target != "" {
		target, targetErr = media.Canonical(target)
		if targetErr == nil && !strings.EqualFold(filepath.Base(target), "Tencent Files") {
			targetErr = errors.New("请选择最外层 Tencent Files 文件夹")
		}
		if targetErr == nil {
			dest, targetErr = os.OpenRoot(target)
		}
	}
	if dest != nil {
		defer dest.Close()
	}
	fallback, err := media.Canonical(fallback)
	if err != nil {
		return result, err
	}
	if target != "" && media.Overlap(target, fallback) {
		return result, errors.New("回退目录不能与 QQ 数据目录重叠")
	}
	if err = os.MkdirAll(fallback, 0700); err != nil {
		return result, err
	}
	library, err := os.OpenRoot(fallback)
	if err != nil {
		return result, err
	}
	defer library.Close()
	byInstance := map[string]Entry{}
	for _, e := range m.Entries {
		byInstance[e.Item.InstanceID] = e
	}
	var bytes int64
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		e := byInstance[item.InstanceID]
		p := Placement{Instance: item.InstanceID, RelativePath: item.RelativePath, SHA256: e.SHA256}
		path, mapErr := mappedPath(m, e)
		writeErr := targetErr
		if mapErr != nil {
			writeErr = mapErr
		}
		if dest != nil && writeErr == nil && item.Status == "recognized" {
			if err = checkTree(dest, filepath.FromSlash(path), true); err == nil {
				var exists bool
				exists, err = sameContent(ctx, dest, filepath.FromSlash(path), e.SHA256, item.Bytes)
				if err == nil && exists {
					p.Root = target
					p.Path = path
					p.Status = "skipped"
					result.Skipped++
				} else if err == nil {
					original := path
					if _, statErr := dest.Lstat(filepath.FromSlash(path)); statErr == nil {
						ext := filepath.Ext(path)
						path = strings.TrimSuffix(path, ext) + "_备份_" + e.SHA256 + ext
					}
					exists, err = sameContent(ctx, dest, filepath.FromSlash(path), e.SHA256, item.Bytes)
					if err == nil && exists {
						p.Status = "skipped"
						result.Skipped++
					} else if err == nil {
						err = copyNew(ctx, item, dest, filepath.FromSlash(path), e.SHA256)
						if err == nil {
							p.Status = "written"
							result.Written++
						}
					}
					if err == nil {
						p.Root = target
						p.Path = path
						if path != original {
							result.Conflicts++
						}
					}
				}
			}
			writeErr = err
		}
		if p.Root == "" {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			path, err = PayloadPath(m, e)
			if err != nil {
				return result, err
			}
			err = checkTree(library, filepath.FromSlash(path), true)
			if err == nil {
				var exists bool
				exists, err = sameContent(ctx, library, filepath.FromSlash(path), e.SHA256, item.Bytes)
				if err == nil && !exists {
					err = copyNew(ctx, item, library, filepath.FromSlash(path), e.SHA256)
				}
			}
			if err != nil {
				result.Failed++
				p.Status = "failed"
				p.Error = "写回与独立保存均未完成"
			} else {
				p.Root = fallback
				p.Path = path
				p.Status = "fallback"
				result.Fallback++
				if writeErr != nil {
					p.Error = writeErr.Error()
				}
			}
		}
		result.Placements = append(result.Placements, p)
		bytes += item.Bytes
		if progress != nil {
			progress(len(result.Placements), bytes)
		}
	}
	if result.Failed > 0 {
		return result, fmt.Errorf("%d 项未能保存；原备份未修改", result.Failed)
	}
	return result, nil
}
