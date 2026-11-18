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
		temporary, e = media.Canonical(temporary)
		if e != nil {
			return nil, e
		}
		if e = os.MkdirAll(temporary, 0700); e != nil {
			return nil, e
		}
		free, e := storage.Free(temporary)
		if e != nil || free < total {
			return nil, ErrPackageSpace
		}
		root, e = os.MkdirTemp(temporary, ".qman-media-stage-unpack-")
		if e != nil {
			return nil, e
		}
		cleanup = func() { safeRemove(root) }
		ok := false
		defer func() {
			if !ok {
				cleanup()
			}
		}()
		if e = storage.Private(root); e != nil {
			return nil, e
		}
		dest, e := os.OpenRoot(root)
		if e != nil {
			return nil, e
		}
		defer dest.Close()
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
			n, e := io.CopyBuffer(out, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(in, int64(entry.UncompressedSize64)+1)}, make([]byte, 1024*1024))
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
			_, e = io.CopyBuffer(entry, media.CancelReader{Ctx: ctx, Reader: in}, buffer)
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
