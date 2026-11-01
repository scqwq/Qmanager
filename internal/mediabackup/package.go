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
	"strings"
)

type Package struct {
	Manifest      Manifest
	Items         []media.Item
	Bytes         []byte
	Root          string
	Cleanup       func()
	MetadataBytes int64
}

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
		if e = checkZIPDirectory(input, info.Size()); e != nil {
			return nil, e
		}
		reader, e := zip.NewReader(input, info.Size())
		if e != nil {
			return nil, e
		}
		names := map[string]*zip.File{}
		folded := map[string]bool{}
		var total uint64
		for _, entry := range reader.File {
			name := strings.TrimSuffix(entry.Name, "/")
			directory := entry.FileInfo().IsDir()
			if !safeRelative(name) || folded[strings.ToLower(name)] || entry.Flags&1 != 0 || entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeType != 0 && !directory || (entry.Method != zip.Store && entry.Method != zip.Deflate) {
				return nil, errors.New("ZIP包含危险、重复、链接、加密或不支持的条目")
			}
			if directory && entry.UncompressedSize64 != 0 {
				return nil, errors.New("ZIP目录包含数据")
			}
			if name != headerName && name != indexName && name != ".qman" && !strings.HasPrefix(name, "Tencent Files/") && name != "Tencent Files" && !strings.HasPrefix(name, "Media/") && name != "Media" {
				return nil, errors.New("ZIP含非目录树备份条目")
			}
			if entry.CompressedSize64 > uint64(info.Size()) || entry.UncompressedSize64 > uint64(maxBytes) || entry.UncompressedSize64 > entry.CompressedSize64*200+maxIndex {
				return nil, errors.New("ZIP长度或压缩比超限")
			}
			if name == headerName && entry.UncompressedSize64 > maxHeader || name == indexName && entry.UncompressedSize64 > maxIndex {
				return nil, errors.New("ZIP元数据超限")
			}
			total += entry.UncompressedSize64
			if total > uint64(maxBytes)+maxIndex+maxHeader {
				return nil, errors.New("ZIP展开总量超限")
			}
			folded[strings.ToLower(name)] = true
			names[name] = entry
		}
		if names[headerName] == nil || names[indexName] == nil {
			return nil, errors.New("ZIP缺少backup.json或逐文件索引；旧格式未适配")
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
			return nil, errors.New("ZIP校验暂存空间不足")
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
		expected, e := expectedTree(m)
		if e != nil {
			return nil, e
		}
		count := 0
		for name, entry := range names {
			if entry.FileInfo().IsDir() {
				if !expected.Dirs[name] {
					return nil, errors.New("ZIP含未知目录")
				}
				continue
			}
			wanted, exists := expected.Files[name]
			if !exists || wanted.Size >= 0 && uint64(wanted.Size) != entry.UncompressedSize64 {
				return nil, errors.New("ZIP载荷集合或长度不符")
			}
			count++
		}
		if count != len(expected.Files) {
			return nil, errors.New("ZIP缺失载荷")
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
	m, _, err := Open(ctx, path, nil)
	if err != nil {
		return "", err
	}
	set, err := expectedTree(m)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(path)
	out, err := os.CreateTemp(parent, ".qman-zip-*.partial")
	if err != nil {
		return "", err
	}
	stage := out.Name()
	defer os.Remove(stage)
	writer := zip.NewWriter(out)
	buffer := make([]byte, 1024*1024)
	for name := range set.Files {
		if err = ctx.Err(); err != nil {
			writer.Close()
			out.Close()
			return "", err
		}
		in, e := media.OpenRelative(path, filepath.FromSlash(name))
		if e != nil {
			writer.Close()
			out.Close()
			return "", e
		}
		entry, e := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if e == nil {
			_, e = io.CopyBuffer(entry, media.CancelReader{Ctx: ctx, Reader: in}, buffer)
		}
		in.Close()
		if e != nil {
			writer.Close()
			out.Close()
			return "", e
		}
	}
	err = writer.Close()
	se := out.Sync()
	ce := out.Close()
	if err != nil || se != nil || ce != nil {
		return "", errors.New("ZIP写入失败")
	}
	verified, err := OpenPackage(ctx, stage, parent, nil)
	if err != nil {
		return "", err
	}
	verified.Cleanup()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	target := path + ".zip"
	if err = storage.Publish(stage, target); err != nil {
		return "", err
	}
	return target, nil
}
