package mediabackup

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
)

type Package struct {
	Manifest      Manifest
	Items         []media.Item
	Bytes         []byte
	Root          string
	Cleanup       func()
	MetadataBytes int64
}

var ErrPackageSpace = errors.New("ZIP校验暂存空间不足")

func OpenPackage(ctx context.Context, path, temporary string, progress func(int, int64)) (*Package, error) {
	path, err := media.Canonical(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	root := path
	cleanup := func() {}
	if info.IsDir() {
		split, e := IsSplit(ctx, path)
		if e != nil {
			return nil, e
		}
		if split {
			m, e := readMetadata(ctx, path)
			if e != nil {
				return nil, e
			}
			return openSplitPackage(ctx, path, temporary, m)
		}
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, errors.New("备份不是普通文件或目录")
		}
		input, e := media.OpenRelative(filepath.Dir(path), filepath.Base(path))
		if e != nil {
			return nil, e
		}
		defer input.Close()
		names, total, e := inspectZIP(ctx, input, info.Size())
		if e != nil {
			return nil, e
		}
		if e = requireZIPMetadata(names); e != nil {
			return nil, e
		}
		root, cleanup, e = unpackStage(temporary, total)
		if e != nil {
			return nil, e
		}
		ok := false
		defer func() {
			if !ok {
				cleanup()
			}
		}()
		dest, e := os.OpenRoot(root)
		if e != nil {
			return nil, e
		}
		defer dest.Close()
		p := media.StartProgress(ctx, "unpacking", int64(total))
		extract := func(name string) error {
			entry := names[name]
			if entry == nil || entry.FileInfo().IsDir() {
				return errors.New("ZIP缺少预期文件")
			}
			if e := checkTree(dest, filepath.FromSlash(name), true); e != nil {
				return e
			}
			in, e := entry.Open()
			if e != nil {
				return e
			}
			defer in.Close()
			out, e := dest.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return e
			}
			n, e := io.CopyBuffer(p.Writer(out), media.CancelReader{Ctx: ctx, Reader: io.LimitReader(in, int64(entry.UncompressedSize64)+1)}, make([]byte, 1024*1024))
			ce := out.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
			if n != int64(entry.UncompressedSize64) {
				return errors.New("ZIP展开长度不符")
			}
			return nil
		}
		if e = extract(headerName); e != nil {
			return nil, e
		}
		if e = extract(indexName); e != nil {
			return nil, e
		}
		m, e := readMetadata(ctx, root)
		if e != nil {
			return nil, e
		}
		if m.Layout != "" {
			return nil, errors.New("分包备份请选择外层文件夹")
		}
		expected, e := verifyZIPTree(names, m)
		if e != nil {
			return nil, e
		}
		for name := range expected.Files {
			if name == headerName || name == indexName {
				continue
			}
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			if e = extract(name); e != nil {
				return nil, e
			}
		}
		ok = true
	}
	m, items, err := Open(ctx, root, progress)
	if err != nil {
		cleanup()
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil || len(b) > maxIndex {
		cleanup()
		return nil, errors.New("备份历史证据超过容量上限")
	}
	var meta int64
	for _, name := range []string{headerName, indexName} {
		i, e := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
		if e != nil {
			cleanup()
			return nil, e
		}
		meta += i.Size()
	}
	return &Package{Manifest: m, Items: items, Bytes: b, Root: root, Cleanup: cleanup, MetadataBytes: meta}, nil
}

func unpackStage(temporary string, total uint64) (string, func(), error) {
	temporary, err := media.Canonical(temporary)
	if err != nil {
		return "", nil, err
	}
	if err = os.MkdirAll(temporary, 0700); err != nil {
		return "", nil, err
	}
	free, err := storage.Free(temporary)
	if err != nil || free < total {
		return "", nil, ErrPackageSpace
	}
	root, err := os.MkdirTemp(temporary, ".qman-media-stage-unpack-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { safeRemove(root) }
	if err = storage.Private(root); err != nil {
		cleanup()
		return "", nil, err
	}
	return root, cleanup, nil
}
func ZipDirectory(ctx context.Context, path string) (string, error) {
	return ZipDirectoryTo(ctx, path, path+".zip")
}

func ZipDirectoryTo(ctx context.Context, path, target string) (string, error) {
	if _, err := WriteVerifiedZIP(ctx, path, target); err != nil {
		return "", err
	}
	return target, nil
}

// WriteVerifiedZIP publishes the archive only after verifying its complete payload.
func WriteVerifiedZIP(ctx context.Context, path, target string) (ZIPVerification, error) {
	m, _, err := Open(ctx, path, nil)
	if err != nil {
		return ZIPVerification{}, err
	}
	set, err := expectedTree(m)
	if err != nil {
		return ZIPVerification{}, err
	}
	parent := filepath.Dir(target)
	out, err := os.CreateTemp(parent, ".qman-zip-*.partial")
	if err != nil {
		return ZIPVerification{}, err
	}
	stage := out.Name()
	defer os.Remove(stage)
	writer := zip.NewWriter(out)
	buffer := make([]byte, 1024*1024)
	var total int64
	for name, file := range set.Files {
		if file.Size >= 0 {
			total += file.Size
			continue
		}
		info, e := os.Lstat(filepath.Join(path, filepath.FromSlash(name)))
		if e != nil {
			out.Close()
			return ZIPVerification{}, e
		}
		total += info.Size()
	}
	p := media.StartProgress(ctx, "packing", total)
	for name := range set.Files {
		if err = ctx.Err(); err != nil {
			writer.Close()
			out.Close()
			return ZIPVerification{}, err
		}
		in, e := media.OpenRelative(path, filepath.FromSlash(name))
		if e != nil {
			writer.Close()
			out.Close()
			return ZIPVerification{}, e
		}
		entry, e := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if e == nil {
			_, e = io.CopyBuffer(p.Writer(entry), media.CancelReader{Ctx: ctx, Reader: in}, buffer)
		}
		in.Close()
		if e != nil {
			writer.Close()
			out.Close()
			return ZIPVerification{}, e
		}
	}
	err = writer.Close()
	se := out.Sync()
	ce := out.Close()
	if err != nil || se != nil || ce != nil {
		return ZIPVerification{}, errors.New("ZIP写入失败")
	}
	verified, err := VerifyZIP(ctx, stage)
	if err != nil {
		return ZIPVerification{}, err
	}
	if err = ctx.Err(); err != nil {
		return ZIPVerification{}, err
	}
	if err = storage.Publish(stage, target); err != nil {
		return ZIPVerification{}, err
	}
	info, err := os.Lstat(target)
	if err != nil || !sameZIP(verified.Info, info) {
		return ZIPVerification{}, errors.New("发布的ZIP与校验文件不一致")
	}
	verified.Info = info
	return verified, nil
}
