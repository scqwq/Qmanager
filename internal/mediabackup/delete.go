package mediabackup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"reflect"
)

// Only journalled quarantine members are removed; never recursively delete a
// tree, follow a link, or accept an unknown addition. Missing known members are
// allowed so a cancelled or crashed deletion can be continued explicitly.
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
		loaded, e := OpenPackage(ctx, path, filepath.Dir(path), nil)
		if e != nil {
			return e
		}
		defer loaded.Cleanup()
		if catalog.ManifestDigest(loaded.Bytes) != row.Digest {
			return errors.New("备份身份不符")
		}
		current, e := parent.Stat(leaf)
		if e != nil || !os.SameFile(info, current) {
			return errors.New("删除对象已变化")
		}
		return parent.Remove(leaf)
	}
	var m Manifest
	if catalog.ManifestDigest([]byte(row.Manifest)) != row.Digest || json.Unmarshal([]byte(row.Manifest), &m) != nil {
		return errors.New("登记证据无效")
	}
	expected, err := expectedTree(m)
	if err != nil {
		return err
	}
	files, dirs, err := inspectTree(ctx, path, expected, true)
	if err != nil {
		return err
	}
	root, err := parent.OpenRoot(leaf)
	if err != nil {
		return err
	}
	defer root.Close()
	pinned, err := root.Stat(".")
	if err != nil || !os.SameFile(info, pinned) {
		return errors.New("删除目录身份变化")
	}
	checkMetadata := func(name string) error {
		if name == headerName {
			b, e := readSmall(ctx, path, name, maxHeader)
			if os.IsNotExist(e) {
				return nil
			}
			if e != nil {
				return e
			}
			var h header
			if decodeStrict(b, &h) != nil || !reflect.DeepEqual(h, m.head()) {
				return errors.New("备份说明已变化")
			}
			return nil
		}
		i, e := root.Stat(filepath.FromSlash(name))
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if i.Size() > maxIndex {
			return errors.New("备份索引超限")
		}
		same, e := sameContent(ctx, root, filepath.FromSlash(name), m.IndexSHA256, i.Size())
		if e != nil {
			return e
		}
		if !same {
			return errors.New("备份索引已变化")
		}
		return nil
	}
	for _, name := range files {
		if name == headerName || name == indexName {
			if err = checkMetadata(name); err != nil {
				return err
			}
			continue
		}
		wanted := expected.Files[name]
		same, e := sameContent(ctx, root, filepath.FromSlash(name), wanted.Hash, wanted.Size)
		if e != nil {
			return e
		}
		if !same {
			return errors.New("备份载荷已变化，停止删除")
		}
	}
	for _, name := range files {
		if name == headerName || name == indexName {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		wanted := expected.Files[name]
		same, e := sameContent(ctx, root, filepath.FromSlash(name), wanted.Hash, wanted.Size)
		if e != nil {
			return e
		}
		if !same {
			return errors.New("删除前载荷变化")
		}
		if err = root.Remove(filepath.FromSlash(name)); err != nil {
			return err
		}
	}
	for _, name := range []string{indexName, headerName} {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = checkMetadata(name); err != nil {
			return err
		}
		if err = root.Remove(filepath.FromSlash(name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for n := len(dirs) - 1; n >= 0; n-- {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = root.Remove(filepath.FromSlash(dirs[n])); err != nil {
			return err
		}
	}
	root.Close()
	current, err := parent.Stat(leaf)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("删除目录在执行时被替换")
	}
	return parent.Remove(leaf)
}
