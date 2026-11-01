package mediabackup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
)

// DeleteQuarantined only removes the validated package set. It never recursively
// removes a directory and does not interpret source-relative paths as targets.
// The caller journals the unique quarantine path before moving anything.
func DeleteQuarantined(ctx context.Context, row catalog.BackupRecord) error {
	if row.DeletePath == "" || filepath.Dir(row.DeletePath) != filepath.Dir(row.Path) {
		return errors.New("删除暂存位置无效")
	}
	path, err := media.Canonical(row.DeletePath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	parent, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	leaf := filepath.Base(path)
	if row.Format == "zip" {
		f, err := parent.Open(leaf)
		if err != nil {
			return err
		}
		before, err := f.Stat()
		f.Close()
		if err != nil || !os.SameFile(info, before) {
			return errors.New("删除对象已变化")
		}
		loaded, err := OpenPackage(ctx, path, filepath.Dir(path), nil)
		if err != nil {
			return err
		}
		defer loaded.Cleanup()
		if catalog.ManifestDigest(loaded.Bytes) != row.Digest {
			return errors.New("备份清单身份不符")
		}
		return parent.Remove(leaf)
	}
	root, err := parent.OpenRoot(leaf)
	if err != nil {
		return err
	}
	defer root.Close()
	var m Manifest
	if err = json.Unmarshal([]byte(row.Manifest), &m); err != nil {
		return err
	}
	expected := map[string]int64{}
	for _, e := range m.Entries {
		if e.Excluded == "" {
			expected[e.SHA256] = e.Item.Bytes
		}
	}
	// Refuse any newly added files before removing any member.
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(4)
	dir.Close()
	if err != nil && err != io.EOF {
		return err
	}
	for _, e := range entries {
		if e.Name() != "objects" && e.Name() != "manifest.json" {
			return errors.New("备份含新增文件，拒绝删除")
		}
	}
	if f, e := media.OpenRelative(path, "manifest.json"); e == nil {
		b, readErr := io.ReadAll(io.LimitReader(f, maxManifest+1))
		f.Close()
		if readErr != nil || len(b) > maxManifest || catalog.ManifestDigest(b) != row.Digest {
			return errors.New("备份清单已变化")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	objects, err := root.OpenRoot("objects")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if objects != nil {
		defer objects.Close()
		dir, err = objects.Open(".")
		if err != nil {
			return err
		}
		defer dir.Close()
		names := []string{}
		for {
			batch, e := dir.ReadDir(128)
			for _, de := range batch {
				size, ok := expected[de.Name()]
				if !ok || !de.Type().IsRegular() {
					return errors.New("备份对象集合发生变化")
				}
				same, e := sameContent(ctx, objects, de.Name(), de.Name(), size)
				if e != nil || !same {
					return errors.New("备份对象已变化，停止删除")
				}
				names = append(names, de.Name())
			}
			if len(batch) == 0 {
				break
			}
			if e != nil {
				return e
			}
		}
		for _, name := range names {
			if err = ctx.Err(); err != nil {
				return err
			}
			same, e := sameContent(ctx, objects, name, name, expected[name])
			if e != nil || !same {
				return errors.New("删除前对象发生变化")
			}
			if err = objects.Remove(name); err != nil {
				return err
			}
		}
		dir.Close()
		objects.Close()
		if err = root.Remove("objects"); err != nil {
			return err
		}
	}
	if err = root.Remove("manifest.json"); err != nil && !os.IsNotExist(err) {
		return err
	}
	root.Close()
	return parent.Remove(leaf)
}
