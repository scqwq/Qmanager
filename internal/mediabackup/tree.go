package mediabackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"strings"
)

type treeFile struct {
	Hash string
	Size int64
}
type treeSet struct {
	Files map[string]treeFile
	Dirs  map[string]bool
}

func expectedTree(m Manifest) (treeSet, error) {
	s := treeSet{Files: map[string]treeFile{headerName: {Size: -1}, indexName: {Hash: m.IndexSHA256, Size: -1}}, Dirs: map[string]bool{}}
	folded := map[string]bool{}
	for _, e := range m.Entries {
		if e.Excluded != "" {
			continue
		}
		p, err := PayloadPath(m, e)
		if err != nil {
			return s, err
		}
		key := strings.ToLower(p)
		if folded[key] {
			return s, errors.New("载荷路径重复")
		}
		folded[key] = true
		s.Files[p] = treeFile{Hash: e.SHA256, Size: e.Item.Bytes}
	}
	for name := range s.Files {
		for d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(name))); d != "."; d = filepath.ToSlash(filepath.Dir(filepath.FromSlash(d))) {
			if _, ok := s.Files[d]; ok {
				return s, errors.New("目录与文件路径冲突")
			}
			s.Dirs[d] = true
		}
	}
	if len(s.Dirs) > maxDirectories+32 {
		return s, errors.New("目录树超过上限")
	}
	return s, nil
}

// Validate the exact whitelist before deletion or loading. Missing members are
// accepted only when resuming a journalled deletion. Links are always rejected.
func inspectTree(ctx context.Context, root string, s treeSet, missing bool) ([]string, []string, error) {
	files, dirs := []string{}, []string{}
	var walk func(string, int) error
	walk = func(rel string, depth int) error {
		if depth > 32 {
			return errors.New("目录树过深")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		dir, err := media.OpenRelative(root, filepath.FromSlash(rel))
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			batch, e := dir.ReadDir(128)
			for _, de := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				p := de.Name()
				if rel != "." {
					p = rel + "/" + p
				}
				if !safeRelative(p) {
					return errors.New("目录树含危险路径")
				}
				if _, err := media.Canonical(filepath.Join(root, filepath.FromSlash(p))); err != nil {
					return err
				}
				info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
				if err != nil {
					return err
				}
				if info.IsDir() {
					if !s.Dirs[p] {
						return errors.New("备份含未知目录")
					}
					dirs = append(dirs, p)
					if err = walk(p, depth+1); err != nil {
						return err
					}
				} else if info.Mode().IsRegular() {
					expected, ok := s.Files[p]
					if !ok || expected.Size >= 0 && info.Size() != expected.Size {
						return errors.New("备份含未知文件或长度不符")
					}
					files = append(files, p)
				} else {
					return errors.New("备份含链接或特殊对象")
				}
				if len(files) > media.MaxFiles+2 || len(dirs) > maxDirectories+32 {
					return errors.New("目录树条目超限")
				}
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, nil, err
	}
	if !missing && (len(files) != len(s.Files) || len(dirs) != len(s.Dirs)) {
		return nil, nil, errors.New("备份文件或目录缺失")
	}
	return files, dirs, nil
}
func verifyFile(ctx context.Context, root, path, hash string, size int64, buffer []byte) (media.Item, error) {
	f, err := media.OpenRelative(root, filepath.FromSlash(path))
	if err != nil {
		return media.Item{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != size {
		return media.Item{}, errors.New("载荷长度/类型不符")
	}
	physical, err := media.AttachObject(media.Item{Bytes: size}, root, filepath.FromSlash(path))
	if err != nil {
		return media.Item{}, err
	}
	same, ok := physical.InfoIdentity.(os.FileInfo)
	if !ok || !os.SameFile(before, same) {
		return media.Item{}, errors.New("载荷被替换")
	}
	digest := sha256.New()
	n, err := io.CopyBuffer(digest, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(f, size+1)}, buffer)
	after, se := f.Stat()
	if err != nil {
		return media.Item{}, err
	}
	if se != nil || n != size || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(digest.Sum(nil)) != hash {
		return media.Item{}, errors.New("载荷SHA-256不符或校验时变化")
	}
	checked, err := media.OpenItem(physical)
	if err != nil {
		return media.Item{}, err
	}
	checked.Close()
	return physical, nil
}
