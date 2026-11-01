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
	"reflect"
	"strings"
)

type Package struct {
	Manifest Manifest
	Items    []media.Item
	Bytes    []byte
	Root     string
	Cleanup  func()
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
		input, err := media.OpenRelative(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return nil, err
		}
		defer input.Close()
		if err = checkZIPDirectory(input, info.Size()); err != nil {
			return nil, err
		}
		reader, err := zip.NewReader(input, info.Size())
		if err != nil {
			return nil, err
		}
		if len(reader.File) > media.MaxFiles+1 {
			return nil, errors.New("ZIP 条目过多")
		}
		names := map[string]bool{}
		var total uint64
		for _, entry := range reader.File {
			name := entry.Name
			if names[name] || entry.Mode()&os.ModeType != 0 || entry.Flags&1 != 0 || (entry.Method != zip.Store && entry.Method != zip.Deflate) {
				return nil, errors.New("ZIP 包含重复、链接、加密或不支持的条目")
			}
			if name != "manifest.json" && !(strings.HasPrefix(name, "objects/") && hashName.MatchString(strings.TrimPrefix(name, "objects/"))) {
				return nil, errors.New("ZIP 包含非备份对象或危险路径")
			}
			if entry.CompressedSize64 > uint64(maxBytes) || entry.CompressedSize64 > uint64(info.Size()) || entry.UncompressedSize64 > uint64(maxBytes) || entry.UncompressedSize64 > entry.CompressedSize64*200+uint64(maxManifest) {
				return nil, errors.New("ZIP 条目超出展开量或压缩比限制")
			}
			if name == "manifest.json" && entry.UncompressedSize64 > uint64(maxManifest) {
				return nil, errors.New("清单超过大小限制")
			}
			names[name] = true
			total += entry.UncompressedSize64
			if total > uint64(maxBytes)+uint64(maxManifest) {
				return nil, errors.New("ZIP 展开量超限")
			}
		}
		if !names["manifest.json"] {
			return nil, errors.New("ZIP 缺少清单")
		}
		temporary, err = media.Canonical(temporary)
		if err != nil {
			return nil, err
		}
		if err = os.MkdirAll(temporary, 0700); err != nil {
			return nil, err
		}
		free, err := storage.Free(temporary)
		if err != nil || free < total {
			return nil, errors.New("ZIP 校验暂存空间不足")
		}
		root, err = os.MkdirTemp(temporary, ".qman-media-stage-unpack-")
		if err != nil {
			return nil, err
		}
		cleanup = func() { safeRemove(root) }
		ok := false
		defer func() {
			if !ok {
				cleanup()
			}
		}()
		if err = storage.Private(root); err != nil {
			return nil, err
		}
		if err = os.Mkdir(filepath.Join(root, "objects"), 0700); err != nil {
			return nil, err
		}
		for _, entry := range reader.File {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			in, e := entry.Open()
			if e != nil {
				return nil, e
			}
			out, e := os.OpenFile(filepath.Join(root, filepath.FromSlash(entry.Name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				in.Close()
				return nil, e
			}
			n, e := io.CopyBuffer(out, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(in, int64(entry.UncompressedSize64)+1)}, make([]byte, 1024*1024))
			inErr := in.Close()
			outErr := out.Close()
			if e != nil || inErr != nil || outErr != nil || n != int64(entry.UncompressedSize64) {
				return nil, errors.New("ZIP 解包失败或长度/CRC 错误")
			}
		}
		ok = true
	}
	m, items, err := Open(ctx, root, progress)
	if err != nil {
		cleanup()
		return nil, err
	}
	mf, err := media.OpenRelative(root, "manifest.json")
	if err != nil {
		cleanup()
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(mf, maxManifest+1))
	mf.Close()
	if err != nil {
		cleanup()
		return nil, err
	}
	var reread Manifest
	if len(b) > maxManifest || json.Unmarshal(b, &reread) != nil || !reflect.DeepEqual(m, reread) {
		cleanup()
		return nil, errors.New("清单在校验时发生变化")
	}
	return &Package{Manifest: m, Items: items, Bytes: b, Root: root, Cleanup: cleanup}, nil
}

// ZipDirectory preserves the self-contained package and stores compressed media
// without recompressing. It never loads an entire object into memory.
func ZipDirectory(ctx context.Context, path string) (string, error) {
	m, _, err := Open(ctx, path, nil)
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
	objects := map[string]bool{}
	names := []string{"manifest.json"}
	for _, e := range m.Entries {
		if e.Excluded == "" && !objects[e.SHA256] {
			objects[e.SHA256] = true
			names = append(names, "objects/"+e.SHA256)
		}
	}
	for _, name := range names {
		in, e := media.OpenRelative(path, filepath.FromSlash(name))
		if e != nil {
			writer.Close()
			out.Close()
			return "", e
		}
		entry, e := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if e == nil {
			_, e = io.CopyBuffer(entry, media.CancelReader{Ctx: ctx, Reader: in}, make([]byte, 1024*1024))
		}
		in.Close()
		if e != nil {
			writer.Close()
			out.Close()
			return "", e
		}
	}
	err = writer.Close()
	syncErr := out.Sync()
	closeErr := out.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return "", errors.New("ZIP 写入失败")
	}
	// Verify CRC and SHA-256 through the exact same bounded reader used on import.
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
