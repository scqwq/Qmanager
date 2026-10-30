// Package mediabackup implements the self-contained Qman directory format.
package mediabackup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"regexp"
	"strings"
	"time"
)

const maxManifest = 32 * 1024 * 1024
const maxBytes = int64(2) * 1024 * 1024 * 1024 * 1024

type Entry struct {
	Item     media.Item        `json:"item"`
	SHA256   string            `json:"sha256"`
	History  []catalog.History `json:"history"`
	Excluded string            `json:"excluded"`
}
type Manifest struct {
	Format     string       `json:"format"`
	Version    int          `json:"version"`
	ID         string       `json:"id"`
	CreatedAt  time.Time    `json:"created_at"`
	SourceID   string       `json:"source_id"`
	SourceName string       `json:"source_name"`
	Selection  media.Filter `json:"selection"`
	Entries    []Entry      `json:"entries"`
}
type Preview struct {
	Files         int   `json:"files"`
	Bytes         int64 `json:"bytes"`
	Excluded      int   `json:"excluded"`
	EstimatedPeak int64 `json:"estimated_peak"`
}
type Result struct {
	Path          string `json:"path"`
	Files         int    `json:"files"`
	UniqueObjects int    `json:"unique_objects"`
	Excluded      int    `json:"excluded"`
	Bytes         int64  `json:"bytes"`
}

func reason(item media.Item) string {
	switch {
	case item.Status != "recognized":
		return item.Status
	case item.Bytes == 0:
		return "empty"
	case item.Role == "temporary":
		return "temporary"
	case item.Stability != "stable_heuristic":
		return "pending"
	}
	return ""
}
func Estimate(items []media.Item, f media.Filter) Preview {
	var p Preview
	for _, item := range items {
		if !media.Matches(item, f) {
			continue
		}
		if reason(item) != "" {
			p.Excluded++
		} else {
			p.Files++
			p.Bytes += item.Bytes
		}
	}
	p.EstimatedPeak = p.Bytes + maxManifest
	return p
}

func Build(ctx context.Context, source media.Source, items []media.Item, f media.Filter, parent string, protected []string, history func(string) ([]catalog.History, error), progress func(int, int64)) (Result, error) {
	var result Result
	target, err := media.Canonical(parent)
	if err != nil {
		return result, err
	}
	for _, root := range append(protected, source.Resolved) {
		if media.Overlap(root, target) {
			return result, errors.New("备份目标必须与媒体来源和应用数据隔离")
		}
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return result, errors.New("备份目标目录不可用")
	}
	p := Estimate(items, f)
	if p.Files == 0 {
		return result, errors.New("当前条件没有可备份的稳定文件，请等待核对或调整筛选")
	}
	if p.Bytes > maxBytes {
		return result, errors.New("备份超过当前容量上限")
	}
	free, err := storage.Free(target)
	if err != nil {
		return result, err
	}
	if free < uint64(p.EstimatedPeak) {
		return result, errors.New("目标可用空间不足（包含清单和暂存预算）")
	}
	stage, err := os.MkdirTemp(target, ".qman-media-stage-")
	if err != nil {
		return result, err
	}
	defer func() {
		if stage != "" {
			safeRemove(stage)
		}
	}()
	if err = storage.Private(stage); err != nil {
		return result, err
	}
	objects := filepath.Join(stage, "objects")
	if err = os.Mkdir(objects, 0700); err != nil {
		return result, err
	}
	m := Manifest{Format: "qman-media-directory", Version: 1, ID: uuid.NewString(), CreatedAt: time.Now().UTC(), SourceID: source.ID, SourceName: source.Name, Selection: f, Entries: []Entry{}}
	unique := map[string]bool{}
	buffer := make([]byte, 1024*1024)
	metadataBytes := 0
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if !media.Matches(item, f) {
			continue
		}
		entry := Entry{Item: item, Excluded: reason(item), History: []catalog.History{}}
		if history != nil {
			entry.History, err = history(item.InstanceID)
			if err != nil {
				return result, err
			}
		}
		encoded, e := json.Marshal(entry)
		if e != nil {
			return result, e
		}
		metadataBytes += len(encoded) + 128
		if metadataBytes > maxManifest-1048576 {
			return result, errors.New("所选观察历史超过清单容量，请缩小选择范围")
		}
		if entry.Excluded != "" {
			m.Entries = append(m.Entries, entry)
			result.Excluded++
			continue
		}
		input, e := media.OpenItem(item)
		if e != nil {
			return result, e
		}
		out, e := os.CreateTemp(objects, ".copy-")
		if e != nil {
			input.Close()
			return result, e
		}
		h := sha256.New()
		n, e := io.CopyBuffer(io.MultiWriter(out, h), media.CancelReader{Ctx: ctx, Reader: input}, buffer)
		stable := media.SameItem(input, item)
		input.Close()
		syncErr := out.Sync()
		closeErr := out.Close()
		pathCheck, pathErr := media.OpenItem(item)
		if pathErr == nil {
			pathCheck.Close()
		}
		if e != nil || syncErr != nil || closeErr != nil || n != item.Bytes || !stable || pathErr != nil {
			return result, errors.New("复制失败、取消或源文件发生变化；未发布备份")
		}
		entry.SHA256 = hex.EncodeToString(h.Sum(nil))
		object := filepath.Join(objects, entry.SHA256)
		if unique[entry.SHA256] {
			if err = os.Remove(out.Name()); err != nil {
				return result, err
			}
		} else {
			if err = storage.Publish(out.Name(), object); err != nil {
				return result, err
			}
			unique[entry.SHA256] = true
			result.Bytes += n
		}
		m.Entries = append(m.Entries, entry)
		result.Files++
		if progress != nil {
			progress(result.Files, result.Bytes)
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return result, err
	}
	if len(b) > maxManifest {
		return result, errors.New("备份清单超出大小上限")
	}
	mf, err := os.OpenFile(filepath.Join(stage, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, err
	}
	_, err = mf.Write(b)
	if err == nil {
		err = mf.Sync()
	}
	closeErr := mf.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	if _, _, err = Open(ctx, stage, nil); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if _, err = media.Canonical(target); err != nil {
		return result, err
	}
	output := filepath.Join(target, "Qman-"+time.Now().Format("20060102-150405")+"-"+m.ID[:8])
	if err = storage.Publish(stage, output); err != nil {
		return result, err
	}
	stage = ""
	result.Path = output
	result.UniqueObjects = len(unique)
	return result, nil
}

var hashName = regexp.MustCompile(`^[0-9a-f]{64}$`)
var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var device = regexp.MustCompile(`^(COM|LPT)[0-9]$`)

func safeRelative(path string) bool {
	if path == "" || path == "." || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") || len(path) > 32768 {
		return false
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if strings.TrimRight(part, " .") != part || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || device.MatchString(stem) {
			return false
		}
	}
	return true
}

func Open(ctx context.Context, path string, progress func(int, int64)) (Manifest, []media.Item, error) {
	var m Manifest
	root, err := media.Canonical(path)
	if err != nil {
		return m, nil, err
	}
	mf, err := media.OpenRelative(root, "manifest.json")
	if err != nil {
		return m, nil, err
	}
	info, err := mf.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifest {
		mf.Close()
		return m, nil, errors.New("清单不是受支持的普通文件或超过大小限制")
	}
	b, err := io.ReadAll(io.LimitReader(media.CancelReader{Ctx: ctx, Reader: mf}, maxManifest+1))
	mf.Close()
	if err != nil {
		return m, nil, err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return m, nil, errors.New("清单格式错误")
	}
	if m.Format != "qman-media-directory" || m.Version != 1 || !safeID.MatchString(m.ID) || len(m.Entries) > media.MaxFiles {
		return m, nil, errors.New("不支持的备份格式、版本或对象数量")
	}
	expected := map[string]int64{}
	instances := map[string]bool{}
	var total int64
	for _, entry := range m.Entries {
		if err = ctx.Err(); err != nil {
			return m, nil, err
		}
		item := entry.Item
		if !safeRelative(item.RelativePath) || !safeID.MatchString(item.InstanceID) || instances[item.InstanceID] || item.Bytes < 0 || item.Bytes > maxBytes || len(entry.History) > 1000 {
			return m, nil, errors.New("清单含危险路径、重复实例或无效长度")
		}
		instances[item.InstanceID] = true
		if entry.Excluded != "" {
			if entry.SHA256 != "" {
				return m, nil, errors.New("未备份项不能引用对象")
			}
			continue
		}
		if !hashName.MatchString(entry.SHA256) {
			return m, nil, errors.New("对象哈希引用无效")
		}
		if old, ok := expected[entry.SHA256]; ok && old != item.Bytes {
			return m, nil, errors.New("同一对象引用长度不一致")
		}
		if _, ok := expected[entry.SHA256]; !ok {
			expected[entry.SHA256] = item.Bytes
			total += item.Bytes
			if total > maxBytes {
				return m, nil, errors.New("备份超过展开量上限")
			}
		}
	}
	rd, err := media.OpenRelative(root, ".")
	if err != nil {
		return m, nil, err
	}
	rootEntries, err := rd.ReadDir(4)
	rd.Close()
	if err != nil && err != io.EOF {
		return m, nil, err
	}
	if len(rootEntries) != 2 {
		return m, nil, errors.New("备份根目录条目集合不符")
	}
	for _, e := range rootEntries {
		if e.Name() != "objects" && e.Name() != "manifest.json" {
			return m, nil, errors.New("备份含额外条目")
		}
	}
	dir, err := media.OpenRelative(root, "objects")
	if err != nil {
		return m, nil, err
	}
	defer dir.Close()
	found := map[string]bool{}
	verified := map[string]media.Item{}
	buffer := make([]byte, 1024*1024)
	var checkedBytes int64
	for {
		entries, e := dir.ReadDir(128)
		for _, de := range entries {
			if err = ctx.Err(); err != nil {
				return m, nil, err
			}
			length, ok := expected[de.Name()]
			if !ok || found[de.Name()] || !hashName.MatchString(de.Name()) {
				return m, nil, errors.New("对象集合不匹配")
			}
			f, err := media.OpenRelative(root, filepath.Join("objects", de.Name()))
			if err != nil {
				return m, nil, err
			}
			before, err := f.Stat()
			if err != nil || !before.Mode().IsRegular() || before.Size() != length {
				f.Close()
				return m, nil, errors.New("对象类型或大小不匹配")
			}
			physical, e := media.AttachObject(media.Item{Bytes: length}, root, filepath.Join("objects", de.Name()))
			if e != nil {
				f.Close()
				return m, nil, e
			}
			physicalInfo, ok := physical.InfoIdentity.(os.FileInfo)
			if !ok || !os.SameFile(before, physicalInfo) {
				f.Close()
				return m, nil, errors.New("对象在校验时被替换")
			}
			h := sha256.New()
			n, err := io.CopyBuffer(h, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(f, length+1)}, buffer)
			after, se := f.Stat()
			f.Close()
			if err != nil || se != nil || n != length || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || hex.EncodeToString(h.Sum(nil)) != de.Name() {
				return m, nil, errors.New("对象 SHA-256 校验失败或校验时发生变化")
			}
			verified[de.Name()] = physical
			found[de.Name()] = true
			checkedBytes += n
			if progress != nil {
				progress(len(found), checkedBytes)
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return m, nil, e
		}
	}
	if len(found) != len(expected) {
		return m, nil, errors.New("备份缺失对象")
	}
	items := []media.Item{}
	for _, entry := range m.Entries {
		if entry.Excluded != "" {
			continue
		}
		physical := verified[entry.SHA256]
		item := entry.Item
		item.Root = physical.Root
		item.Path = physical.Path
		item.InfoIdentity = physical.InfoIdentity
		item.PhysicalModifiedAt = physical.PhysicalModifiedAt
		item.PhysicalIdentity = physical.PhysicalIdentity
		item.Format = physical.Format
		item.Status = physical.Status
		item.Preview = physical.Preview
		item.ID = rand.Text()
		items = append(items, item)
	}
	return m, items, nil
}

// Only this job's fresh directory is cleaned, without following links.
func safeRemove(path string) {
	root, err := media.Canonical(path)
	if err != nil || !strings.HasPrefix(filepath.Base(root), ".qman-media-stage-") {
		return
	}
	var files, dirs []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if _, e = media.Canonical(p); e != nil {
			return e
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		} else if d.Type().IsRegular() {
			files = append(files, p)
		} else {
			return fmt.Errorf("unexpected stage entry")
		}
		return nil
	})
	if err != nil {
		return
	}
	for _, p := range files {
		if _, e := media.Canonical(p); e != nil {
			return
		}
		if os.Remove(p) != nil {
			return
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if os.Remove(dirs[i]) != nil {
			return
		}
	}
}
