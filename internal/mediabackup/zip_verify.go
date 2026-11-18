package mediabackup

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"qqarchive/internal/media"
)

type ZIPMetadata struct {
	Manifest Manifest
	Bytes    []byte
	Info     os.FileInfo
}

type ZIPVerification struct {
	ZIPMetadata
	verified bool
}

func inspectZIP(ctx context.Context, input io.ReaderAt, size int64) (map[string]*zip.File, uint64, error) {
	if err := checkZIPDirectory(input, size); err != nil {
		return nil, 0, err
	}
	reader, err := zip.NewReader(input, size)
	if err != nil {
		return nil, 0, err
	}
	names := make(map[string]*zip.File, len(reader.File))
	folded := make(map[string]bool, len(reader.File))
	var total uint64
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		directory := entry.FileInfo().IsDir()
		key := strings.ToLower(name)
		if !safeRelative(name) || folded[key] || entry.Flags&1 != 0 || entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeType != 0 && !directory || (entry.Method != zip.Store && entry.Method != zip.Deflate) {
			return nil, 0, errors.New("ZIP包含危险、重复、链接、加密或不支持的条目")
		}
		if directory && entry.UncompressedSize64 != 0 {
			return nil, 0, errors.New("ZIP目录包含数据")
		}
		if name != headerName && name != indexName && name != ".qman" && !strings.HasPrefix(name, "Tencent Files/") && name != "Tencent Files" && !strings.HasPrefix(name, "Media/") && name != "Media" {
			return nil, 0, errors.New("ZIP含非目录树备份条目")
		}
		if entry.CompressedSize64 > uint64(size) || entry.UncompressedSize64 > uint64(maxBytes) || entry.UncompressedSize64 > entry.CompressedSize64*200+maxIndex {
			return nil, 0, errors.New("ZIP长度或压缩比超限")
		}
		if name == headerName && entry.UncompressedSize64 > maxHeader || name == indexName && entry.UncompressedSize64 > maxIndex {
			return nil, 0, errors.New("ZIP元数据超限")
		}
		total += entry.UncompressedSize64
		if total > uint64(maxBytes)+maxIndex+maxHeader {
			return nil, 0, errors.New("ZIP展开总量超限")
		}
		folded[key] = true
		names[name] = entry
	}
	if names[headerName] == nil || names[indexName] == nil {
		return nil, 0, errors.New("ZIP缺少backup.json或逐文件索引；旧格式未适配")
	}
	return names, total, nil
}

func verifyZIPTree(names map[string]*zip.File, m Manifest) (treeSet, error) {
	expected, err := expectedTree(m)
	if err != nil {
		return expected, err
	}
	for dir := range expected.Dirs {
		if strings.Count(dir, "/") >= 32 {
			return expected, errors.New("备份目录树过深")
		}
	}
	count := 0
	for name, entry := range names {
		if entry.FileInfo().IsDir() {
			if !expected.Dirs[name] {
				return expected, errors.New("ZIP含未知目录")
			}
			continue
		}
		wanted, exists := expected.Files[name]
		if !exists || wanted.Size >= 0 && uint64(wanted.Size) != entry.UncompressedSize64 {
			return expected, errors.New("ZIP载荷集合或长度不符")
		}
		count++
	}
	if count != len(expected.Files) {
		return expected, errors.New("ZIP缺失载荷")
	}
	return expected, nil
}

// VerifyZIP checks the complete package and keeps the payload inside the archive.
func VerifyZIP(ctx context.Context, path string) (ZIPVerification, error) {
	metadata, err := readZIP(ctx, path, true)
	return ZIPVerification{ZIPMetadata: metadata, verified: err == nil}, err
}

// PreviewZIP reads the archive directory and manifest without reading media payloads.
func PreviewZIP(ctx context.Context, path string) (ZIPMetadata, error) {
	return readZIP(ctx, path, false)
}

func readZIP(ctx context.Context, path string, verify bool) (ZIPMetadata, error) {
	path, err := media.Canonical(path)
	if err != nil {
		return ZIPMetadata{}, err
	}
	input, err := media.OpenRelative(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return ZIPMetadata{}, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ZIPMetadata{}, errors.New("备份ZIP不是可读的普通文件")
	}
	names, _, err := inspectZIP(ctx, input, info.Size())
	if err != nil {
		return ZIPMetadata{}, err
	}
	m, err := decodeMetadata(ctx, func(name string, limit int64) (io.ReadCloser, error) {
		entry := names[name]
		if entry.FileInfo().IsDir() || entry.UncompressedSize64 > uint64(limit) {
			return nil, errors.New("ZIP元数据超限或不是普通文件")
		}
		return entry.Open()
	})
	if err != nil {
		return ZIPMetadata{}, err
	}
	tree, err := verifyZIPTree(names, m)
	if err != nil {
		return ZIPMetadata{}, err
	}
	if verify {
		if err := verifyZIPPayload(ctx, names, tree); err != nil {
			return ZIPMetadata{}, err
		}
	}
	current, err := os.Lstat(path)
	if err != nil || !sameZIP(info, current) {
		return ZIPMetadata{}, errors.New("备份ZIP在核对期间已移动或变化")
	}
	b, err := json.Marshal(m)
	if err != nil || len(b) > maxIndex {
		return ZIPMetadata{}, errors.New("备份历史证据超过容量上限")
	}
	return ZIPMetadata{Manifest: m, Bytes: b, Info: info}, nil
}

func verifyZIPPayload(ctx context.Context, names map[string]*zip.File, tree treeSet) error {
	buffer := make([]byte, 1024*1024)
	digest := sha256.New()
	for name, expected := range tree.Files {
		if name == headerName || name == indexName {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entry, err := names[name].Open()
		if err != nil {
			return err
		}
		digest.Reset()
		n, err := io.CopyBuffer(digest, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(entry, expected.Size+1)}, buffer)
		entry.Close()
		if err != nil {
			return err
		}
		if n != expected.Size || hex.EncodeToString(digest.Sum(nil)) != expected.Hash {
			return errors.New("ZIP载荷长度或SHA-256不符")
		}
	}
	return nil
}

func (v ZIPVerification) Matches(info os.FileInfo) bool {
	return v.verified && v.Info != nil && info != nil && sameZIP(v.Info, info)
}

func sameZIP(before, after os.FileInfo) bool {
	return after.Mode().IsRegular() && os.SameFile(before, after) &&
		after.Size() == before.Size() && after.ModTime().Equal(before.ModTime())
}
