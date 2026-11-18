package mediabackup

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"qqarchive/internal/media"
	"qqarchive/internal/storage"
)

const SplitZIP = "split_zip"
const SplitZIPLimit int64 = 4_800_000_000
const splitVersion = 3
const maxSplitParts = 1024
const splitDirectoryLimit = 128 * 1024 * 1024

// Keep a conservative bound for a Store entry, including its UTF-8 name
// twice, a ZIP64 data descriptor and the ZIP64 central directory extra field.
// Names are the only variable metadata written by writePart.
const splitZIPFooter = 98

type SplitPart struct {
	Name   string `json:"name"`
	Size   int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type SplitMetadata struct {
	Manifest Manifest
	Bytes    []byte
	Size     int64
	members  map[string]os.FileInfo
}

func (m SplitMetadata) Matches(path string) bool {
	if len(m.members) != len(m.Manifest.Parts)+2 {
		return false
	}
	for name, before := range m.members {
		f, err := media.OpenRelative(path, filepath.FromSlash(name))
		if err != nil {
			return false
		}
		after, err := f.Stat()
		f.Close()
		if err != nil || !sameZIP(before, after) {
			return false
		}
	}
	return true
}

func splitPartName(n int) string {
	return fmt.Sprintf("part-%03d.zip", n+1)
}

func validSplitParts(parts []SplitPart, files int) bool {
	if len(parts) == 0 || len(parts) > files || len(parts) > maxSplitParts {
		return false
	}
	for n, part := range parts {
		if part.Name != splitPartName(n) || part.Size <= 0 || part.Size > SplitZIPLimit || !hashName.MatchString(part.SHA256) {
			return false
		}
	}
	return true
}

func zipEntryBound(name string, size int64) int64 {
	return size + 30 + 46 + 24 + 28 + 2*int64(len(name))
}

func checkSplitSize(name string, size, limit int64) error {
	if splitZIPFooter+zipEntryBound(name, size) > limit {
		return fmt.Errorf("单个文件超过分包上限：%s。请调整范围或选择文件夹备份", name)
	}
	return nil
}

func planSplit(tree treeSet, limit int64) ([][]string, error) {
	names := make([]string, 0, len(tree.Files))
	for name := range tree.Files {
		if name != headerName && name != indexName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var parts [][]string
	start, bytes := 0, int64(splitZIPFooter)
	directoryBytes := 0
	for i, name := range names {
		file := tree.Files[name]
		if err := checkSplitSize(name, file.Size, limit); err != nil {
			return nil, err
		}
		entry := zipEntryBound(name, file.Size)
		directoryEntry := 46 + 28 + len(name)
		if bytes+entry > limit || directoryBytes+directoryEntry > splitDirectoryLimit {
			parts = append(parts, names[start:i])
			start, bytes = i, splitZIPFooter
			directoryBytes = 0
		}
		bytes += entry
		directoryBytes += directoryEntry
	}
	if start < len(names) {
		parts = append(parts, names[start:])
	}
	if len(parts) == 0 || len(parts) > maxSplitParts {
		return nil, errors.New("分包数量超限，请缩小备份范围")
	}
	return parts, nil
}

func writeSplit(ctx context.Context, source, target string, m Manifest, limit int64) (SplitMetadata, error) {
	tree, err := expectedTree(m)
	if err != nil {
		return SplitMetadata{}, err
	}
	plans, err := planSplit(tree, limit)
	if err != nil {
		return SplitMetadata{}, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".qman-media-stage-split-")
	if err != nil {
		return SplitMetadata{}, err
	}
	defer safeRemove(stage)
	if err = storage.Private(stage); err != nil {
		return SplitMetadata{}, err
	}
	m.Layout, m.Version = SplitZIP, splitVersion
	p := media.StartProgress(ctx, "packing", m.head().Bytes)
	buffer := make([]byte, 1024*1024)
	for i, paths := range plans {
		p.Part(i+1, len(plans))
		part, err := writePart(ctx, source, stage, splitPartName(i), paths, tree, limit, p, buffer)
		if err != nil {
			return SplitMetadata{}, err
		}
		m.Parts = append(m.Parts, part)
	}
	if err = os.Mkdir(filepath.Join(stage, ".qman"), 0700); err != nil {
		return SplitMetadata{}, err
	}
	index, err := openMetadataFile(source, indexName, maxIndex)
	if err != nil {
		return SplitMetadata{}, err
	}
	err = copySplitIndex(ctx, stage, index, buffer)
	index.Close()
	if err != nil {
		return SplitMetadata{}, err
	}
	if err = writeHeader(stage, m); err != nil {
		return SplitMetadata{}, err
	}
	metadata, err := InspectSplit(ctx, stage, true)
	if err != nil {
		return SplitMetadata{}, err
	}
	if err = publishTree(ctx, stage, target); err != nil {
		return SplitMetadata{}, err
	}
	return metadata, nil
}

func copySplitIndex(ctx context.Context, stage string, index io.Reader, buffer []byte) error {
	out, err := os.OpenFile(filepath.Join(stage, filepath.FromSlash(indexName)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(out, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(index, maxIndex+1)}, buffer)
	return errors.Join(err, out.Sync(), out.Close())
}

func writePart(ctx context.Context, source, stage, name string, paths []string, tree treeSet, limit int64, p *media.Progress, buffer []byte) (SplitPart, error) {
	out, err := os.OpenFile(filepath.Join(stage, name), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return SplitPart{}, err
	}
	defer out.Close()
	hash := sha256.New()
	w := zip.NewWriter(io.MultiWriter(out, hash))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return SplitPart{}, err
		}
		entry, err := w.CreateHeader(&zip.FileHeader{Name: path, Method: zip.Store})
		if err != nil {
			return SplitPart{}, err
		}
		in, err := media.OpenRelative(source, filepath.FromSlash(path))
		if err != nil {
			return SplitPart{}, err
		}
		expected := tree.Files[path]
		n, err := io.CopyBuffer(p.Writer(entry), media.CancelReader{Ctx: ctx, Reader: io.LimitReader(in, expected.Size+1)}, buffer)
		in.Close()
		if err != nil {
			return SplitPart{}, err
		}
		if n != expected.Size {
			return SplitPart{}, errors.New("分包时媒体长度变化")
		}
	}
	if err := w.Close(); err != nil {
		return SplitPart{}, err
	}
	if err := out.Sync(); err != nil {
		return SplitPart{}, err
	}
	info, err := out.Stat()
	if err != nil {
		return SplitPart{}, err
	}
	if info.Size() > limit {
		return SplitPart{}, errors.New("分包 ZIP 成品超过大小上限")
	}
	if err := out.Close(); err != nil {
		return SplitPart{}, err
	}
	return SplitPart{Name: name, Size: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func splitTree(m Manifest) treeSet {
	tree := treeSet{Files: map[string]treeFile{
		headerName: {Size: -1},
		indexName:  {Hash: m.IndexSHA256, Size: -1},
	}, Dirs: map[string]bool{".qman": true}}
	for _, part := range m.Parts {
		tree.Files[part.Name] = treeFile{Hash: part.SHA256, Size: part.Size}
	}
	return tree
}

func InspectSplit(ctx context.Context, path string, full bool) (SplitMetadata, error) {
	m, err := readMetadata(ctx, path)
	if err != nil {
		return SplitMetadata{}, err
	}
	return inspectSplit(ctx, path, m, full, nil)
}

func SplitUploadFiles(ctx context.Context, path string, m Manifest) (map[string]string, error) {
	data, err := readSmall(ctx, path, headerName, maxSplitHeader)
	if err != nil {
		return nil, err
	}
	var current header
	if err := decodeStrict(data, &current); err != nil || !reflect.DeepEqual(current, m.head()) {
		return nil, errors.New("分包说明已变化")
	}
	sum := sha256.Sum256(data)
	files := map[string]string{headerName: hex.EncodeToString(sum[:]), indexName: m.IndexSHA256}
	for _, part := range m.Parts {
		files[part.Name] = part.SHA256
	}
	return files, nil
}

func IsSplit(ctx context.Context, path string) (bool, error) {
	b, err := readSmall(ctx, path, headerName, maxSplitHeader)
	if err != nil {
		return false, err
	}
	var h header
	if err := decodeStrict(b, &h); err != nil {
		return false, err
	}
	return h.Layout == SplitZIP, nil
}

func inspectSplit(ctx context.Context, path string, m Manifest, full bool, dest *os.Root) (SplitMetadata, error) {
	if m.Layout != SplitZIP {
		return SplitMetadata{}, errors.New("该目录不是分包 ZIP 备份")
	}
	tree := splitTree(m)
	if _, _, err := inspectTree(ctx, path, tree, false); err != nil {
		return SplitMetadata{}, err
	}
	payload, err := expectedTree(m)
	if err != nil {
		return SplitMetadata{}, err
	}
	delete(payload.Files, headerName)
	delete(payload.Files, indexName)
	var total int64
	for _, part := range m.Parts {
		total += part.Size
	}
	var p *media.Progress
	if full {
		stage := "verifying"
		if dest != nil {
			stage = "unpacking"
		}
		p = media.StartProgress(ctx, stage, total+m.head().Bytes)
	}
	buffer := make([]byte, 1024*1024)
	members := make(map[string]os.FileInfo, len(m.Parts)+2)
	for i, part := range m.Parts {
		if err := ctx.Err(); err != nil {
			return SplitMetadata{}, err
		}
		if p != nil {
			p.Part(i+1, len(m.Parts))
		}
		if err := inspectPart(ctx, path, part, payload.Files, p, dest, buffer, members); err != nil {
			return SplitMetadata{}, fmt.Errorf("%s：%w", part.Name, err)
		}
	}
	if len(payload.Files) != 0 {
		return SplitMetadata{}, errors.New("分包备份缺少清单中的媒体")
	}
	if full {
		reread, err := readMetadata(ctx, path)
		if err != nil || !reflect.DeepEqual(m.head(), reread.head()) {
			return SplitMetadata{}, errors.New("分包清单在核对期间已变化")
		}
		if _, _, err := inspectTree(ctx, path, tree, false); err != nil {
			return SplitMetadata{}, err
		}
	}
	encoded, err := json.Marshal(m)
	if err != nil || len(encoded) > maxIndex {
		return SplitMetadata{}, errors.New("分包备份历史证据超过上限")
	}
	for _, name := range []string{headerName, indexName} {
		f, e := media.OpenRelative(path, filepath.FromSlash(name))
		if e != nil {
			return SplitMetadata{}, e
		}
		info, e := f.Stat()
		f.Close()
		if e != nil {
			return SplitMetadata{}, e
		}
		total += info.Size()
		members[name] = info
	}
	return SplitMetadata{Manifest: m, Bytes: encoded, Size: total, members: members}, nil
}

func inspectPart(ctx context.Context, path string, part SplitPart, remaining map[string]treeFile, p *media.Progress, dest *os.Root, buffer []byte, members map[string]os.FileInfo) error {
	in, err := media.OpenRelativeWithoutWriters(path, part.Name)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != part.Size {
		return errors.New("分包文件类型或大小与清单不符")
	}
	names, _, err := inspectZIP(ctx, in, part.Size)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("分包 ZIP 为空")
	}
	if p != nil {
		digest := sha256.New()
		n, err := io.CopyBuffer(p.Writer(digest), media.CancelReader{Ctx: ctx, Reader: io.NewSectionReader(in, 0, part.Size)}, buffer)
		if err != nil {
			return err
		}
		if n != part.Size || hex.EncodeToString(digest.Sum(nil)) != part.SHA256 {
			return errors.New("分包 SHA-256 与清单不符")
		}
	}
	for name, file := range names {
		expected, ok := remaining[name]
		if !ok || file.FileInfo().IsDir() || file.Method != zip.Store || file.UncompressedSize64 != uint64(expected.Size) || file.CompressedSize64 != uint64(expected.Size) {
			return errors.New("分包含额外、重复或尺寸不符的文件")
		}
		if p != nil {
			if err := readPartEntry(ctx, file, name, expected, p, dest, buffer); err != nil {
				return err
			}
		}
		delete(remaining, name)
	}
	current, err := os.Lstat(filepath.Join(path, part.Name))
	if err != nil || !sameZIP(info, current) {
		return errors.New("分包在核对期间已移动或变化")
	}
	members[part.Name] = info
	return nil
}

func readPartEntry(ctx context.Context, file *zip.File, name string, expected treeFile, p *media.Progress, dest *os.Root, buffer []byte) error {
	in, err := file.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	digest := sha256.New()
	var writer io.Writer = digest
	var out *os.File
	if dest != nil {
		rel := filepath.FromSlash(name)
		if err := checkTree(dest, rel, true); err != nil {
			return err
		}
		out, err = dest.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		writer = io.MultiWriter(out, digest)
	}
	n, err := io.CopyBuffer(p.Writer(writer), media.CancelReader{Ctx: ctx, Reader: io.LimitReader(in, expected.Size+1)}, buffer)
	if out != nil {
		err = errors.Join(err, out.Close())
	}
	if err != nil {
		return err
	}
	if n != expected.Size || hex.EncodeToString(digest.Sum(nil)) != expected.Hash {
		return errors.New("分包媒体内容与逐文件索引不符")
	}
	return nil
}

func openSplitPackage(ctx context.Context, path, temporary string, m Manifest) (*Package, error) {
	stage, cleanup, err := unpackStage(temporary, uint64(m.head().Bytes))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	dest, err := os.OpenRoot(stage)
	if err != nil {
		return nil, err
	}
	defer dest.Close()
	metadata, err := inspectSplit(ctx, path, m, true, dest)
	if err != nil {
		return nil, err
	}
	items := make([]media.Item, 0, m.head().Files)
	for _, e := range m.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Excluded != "" {
			continue
		}
		name, err := PayloadPath(m, e)
		if err != nil {
			return nil, err
		}
		item, err := media.AttachObject(e.Item, stage, filepath.FromSlash(name))
		if err != nil {
			return nil, err
		}
		item.ID = rand.Text()
		items = append(items, item)
	}
	ok = true
	return &Package{Manifest: m, Items: items, Bytes: metadata.Bytes, Root: stage, Cleanup: cleanup, MetadataBytes: metadata.Size}, nil
}
