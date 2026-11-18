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
	return deleteQuarantined(ctx, row, ZIPVerification{})
}

// DeleteVerifiedZIP reuses this operation's verification across the quarantine rename.
// The caller keeps the checked ZIP open against writes until removal returns.
func DeleteVerifiedZIP(ctx context.Context, row catalog.BackupRecord, verified ZIPVerification) error {
	if row.Format != "zip" || !verified.verified {
		return errors.New("缺少本次ZIP删除校验证据")
	}
	return deleteQuarantined(ctx, row, verified)
}

func deleteQuarantined(ctx context.Context, row catalog.BackupRecord, verified ZIPVerification) error {
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
		pinned, e := media.OpenRelativeWithoutWriters(filepath.Dir(path), leaf)
		if e != nil {
			return e
		}
		defer pinned.Close()
		if !verified.verified {
			verified, e = VerifyZIP(ctx, path)
			if e != nil {
				return e
			}
		}
		if verified.Manifest.ID != row.ID || catalog.ManifestDigest(verified.Bytes) != row.Digest {
			return errors.New("备份身份不符")
		}
		pinnedInfo, e := pinned.Stat()
		if e != nil || !verified.Matches(pinnedInfo) || !verified.Matches(info) {
			return errors.New("删除对象已变化")
		}
		current, e := parent.Lstat(leaf)
		if e != nil || !verified.Matches(current) {
			return errors.New("删除对象已变化")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return parent.Remove(leaf)
	}
	var m Manifest
	if catalog.ManifestDigest([]byte(row.Manifest)) != row.Digest || json.Unmarshal([]byte(row.Manifest), &m) != nil {
		return errors.New("登记证据无效")
	}
	var expected treeSet
	headerLimit := int64(maxHeader)
	if row.Format == SplitZIP && m.Layout == SplitZIP {
		expected, headerLimit = splitTree(m), maxSplitHeader
	} else if row.Format == "directory" && m.Layout == "" {
		expected, err = expectedTree(m)
		if err != nil {
			return err
		}
	} else {
		return errors.New("备份类型与删除登记不符")
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
			b, e := readSmall(ctx, path, name, headerLimit)
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
	var total int64
	for _, name := range files {
		total += max(0, expected.Files[name].Size)
	}
	checking := media.StartProgress(ctx, "verifying", total)
	for _, name := range files {
		if name == headerName || name == indexName {
			if err = checkMetadata(name); err != nil {
				return err
			}
			continue
		}
		wanted := expected.Files[name]
		same, e := checkContent(ctx, root, filepath.FromSlash(name), wanted.Hash, wanted.Size, checking)
		if e != nil {
			return e
		}
		if !same {
			return errors.New("备份载荷已变化，停止删除")
		}
	}
	deleting := media.StartProgress(ctx, "deleting", total)
	for _, name := range files {
		if name == headerName || name == indexName {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		wanted := expected.Files[name]
		same, e := checkContent(ctx, root, filepath.FromSlash(name), wanted.Hash, wanted.Size, deleting)
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
