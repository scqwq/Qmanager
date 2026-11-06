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
	"reflect"
	"regexp"
	"strings"
	"time"
)

const maxManifest = maxIndex
const maxBytes = int64(2) * 1024 * 1024 * 1024 * 1024

type Entry struct {
	Item     media.Item        `json:"item"`
	SHA256   string            `json:"sha256"`
	History  []catalog.History `json:"history"`
	Excluded string            `json:"excluded"`
}
type Manifest struct {
	PayloadRoot   string       `json:"payload_root"`
	IndexSHA256   string       `json:"index_sha256"`
	RestorePrefix string       `json:"restore_prefix,omitempty"`
	Format        string       `json:"format"`
	Version       int          `json:"version"`
	ID            string       `json:"id"`
	CreatedAt     time.Time    `json:"created_at"`
	SourceID      string       `json:"source_id"`
	SourceName    string       `json:"source_name"`
	Selection     media.Filter `json:"selection"`
	Entries       []Entry      `json:"entries"`
}
type Preview struct {
	Pending         int            `json:"pending"`
	PendingBytes    int64          `json:"pending_bytes"`
	ExcludedReasons map[string]int `json:"excluded_reasons"`
	Files           int            `json:"files"`
	Bytes           int64          `json:"bytes"`
	Excluded        int            `json:"excluded"`
	EstimatedPeak   int64          `json:"estimated_peak"`
}
type Result struct {
	StagingRemainder string `json:"-"`
	Path             string `json:"path"`
	Files            int    `json:"files"`
	PayloadFiles     int    `json:"payload_files"`
	Excluded         int    `json:"excluded"`
	Bytes            int64  `json:"bytes"`
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
	return estimate(items, f, nil)
}

type BuildOptions struct{ Excluded map[string]string }

func exclusion(item media.Item, extra map[string]string) string {
	if why := extra[item.InstanceID]; why != "" {
		return why
	}
	return reason(item)
}
func estimate(items []media.Item, f media.Filter, extra map[string]string) Preview {
	var p Preview
	for _, item := range items {
		if !media.Matches(item, f) {
			continue
		}
		if why := exclusion(item, extra); why != "" {
			p.Excluded++
			if p.ExcludedReasons == nil {
				p.ExcludedReasons = map[string]int{}
			}
			p.ExcludedReasons[why]++
		} else {
			p.Files++
			p.Bytes += item.Bytes
		}
	}
	if p.Files > 0 {
		p.EstimatedPeak = DirectoryBudgetFor(p.Files+p.Excluded, p.Bytes)
	}
	return p
}

// Plan separates pending candidates from known exclusions. Pending candidates
// require real on-demand observations; this does not grant Build eligibility.
func Plan(items []media.Item, f media.Filter) Preview {
	p := Estimate(items, f)
	p.Pending = p.ExcludedReasons["pending"]
	p.Excluded -= p.Pending
	delete(p.ExcludedReasons, "pending")
	for _, item := range items {
		if media.Matches(item, f) && reason(item) == "pending" {
			p.PendingBytes += item.Bytes
		}
	}
	if p.Files+p.Pending > 0 {
		p.EstimatedPeak = DirectoryBudgetFor(p.Files+p.Pending+p.Excluded, p.Bytes+p.PendingBytes)
	}
	return p
}

// Includes a conservative manifest reserve, rather than claiming an exact size.
func DirectoryBudgetFor(entries int, bytes int64) int64 {
	reserve := int64(maxHeader) + int64(entries)*2048
	if reserve > maxIndex {
		reserve = maxIndex
	}
	return bytes + reserve
}

func Build(ctx context.Context, source media.Source, items []media.Item, f media.Filter, parent string, protected []string, history func(string) ([]catalog.History, error), progress func(int, int64), options ...BuildOptions) (result Result, err error) {
	if err := media.ValidateFilter(f); err != nil {
		return result, err
	}
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
	var extra map[string]string
	if len(options) > 0 {
		extra = options[0].Excluded
	}
	p := estimate(items, f, extra)
	if p.Files == 0 {
		return result, errors.New("当前条件没有可备份的稳定文件")
	}
	if p.Bytes > maxBytes {
		return result, errors.New("备份超过容量上限")
	}
	free, err := storage.Free(target)
	if err != nil || free < uint64(p.EstimatedPeak) {
		return result, errors.New("目标可用空间不足或无法查询")
	}
	stage, err := os.MkdirTemp(target, ".qman-media-stage-")
	if err != nil {
		return result, err
	}
	defer func() {
		if stage != "" {
			safeRemove(stage)
			result.StagingRemainder = remainingStage(stage)
		}
	}()
	if err = storage.Private(stage); err != nil {
		return result, fmt.Errorf("设置备份暂存目录访问范围: %w", err)
	}
	m := Manifest{Format: Format, Version: Version, ID: uuid.NewString(), CreatedAt: time.Now().UTC(), SourceID: source.ID, SourceName: source.Name, Selection: f, RestorePrefix: restorePrefix(source.Resolved), PayloadRoot: "Media", Entries: []Entry{}}
	if m.RestorePrefix != "" {
		m.PayloadRoot = "Tencent Files"
	} else if len(source.Scopes) == 1 && source.Scopes[0].Prefix == "" {
		for _, root := range []string{"Pic", "Video", "Ptt", "Emoji/emoji-recv"} {
			if strings.EqualFold(filepath.Base(source.Resolved), filepath.Base(filepath.FromSlash(root))) {
				m.PayloadRoot = "Media/" + root
				break
			}
		}
	}
	names := map[string]bool{}
	metadataBytes := 0
	buffer := make([]byte, 1024*1024)
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if !media.Matches(item, f) {
			continue
		}
		if len(m.Entries) >= media.MaxFiles || !safeRelative(item.RelativePath) || !safeID.MatchString(item.InstanceID) {
			return result, errors.New("所选文件路径、实例或数量无效")
		}
		key := strings.ToLower(item.RelativePath)
		if names[key] {
			return result, errors.New("所选文件路径重复")
		}
		names[key] = true
		entry := Entry{Item: item, Excluded: exclusion(item, extra), History: []catalog.History{}}
		if history != nil {
			entry.History, err = history(item.InstanceID)
			if err != nil {
				return result, err
			}
		}
		encoded, e := json.Marshal(compact(entry))
		if e != nil {
			return result, e
		}
		metadataBytes += len(encoded) + 128
		if len(encoded) > maxLine-128 || metadataBytes > maxIndex-1048576 {
			return result, errors.New("所选观察证据超过索引容量，请缩小范围")
		}
		if entry.Excluded != "" {
			m.Entries = append(m.Entries, entry)
			result.Excluded++
			continue
		}
		path, e := PayloadPath(m, entry)
		if e != nil {
			return result, e
		}
		if e = os.MkdirAll(filepath.Dir(filepath.Join(stage, filepath.FromSlash(path))), 0700); e != nil {
			return result, e
		}
		input, e := media.OpenItem(item)
		if e != nil {
			return result, fmt.Errorf("读取所选媒体: %w", e)
		}
		out, e := os.OpenFile(filepath.Join(stage, filepath.FromSlash(path)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			input.Close()
			return result, e
		}
		hash := sha256.New()
		n, e := io.CopyBuffer(io.MultiWriter(out, hash), media.CancelReader{Ctx: ctx, Reader: io.LimitReader(input, item.Bytes+1)}, buffer)
		stable := media.SameItem(input, item)
		input.Close()
		se := out.Sync()
		ce := out.Close()
		checked, pe := media.OpenItem(item)
		if checked != nil {
			checked.Close()
		}
		if e != nil || se != nil || ce != nil || pe != nil || n != item.Bytes || !stable {
			return result, errors.New("复制失败、取消或源文件发生变化；未发布备份")
		}
		entry.SHA256 = hex.EncodeToString(hash.Sum(nil))
		m.Entries = append(m.Entries, entry)
		result.Files++
		result.PayloadFiles++
		result.Bytes += n
		if progress != nil {
			progress(result.Files, result.Bytes)
		}
	}
	if err = writeMetadata(ctx, stage, &m); err != nil {
		return result, fmt.Errorf("写入备份说明和索引: %w", err)
	}
	if _, _, err = Open(ctx, stage, nil); err != nil {
		return result, fmt.Errorf("发布前校验目录树: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if _, err = media.Canonical(target); err != nil {
		return result, err
	}
	output := filepath.Join(target, "Qman-"+time.Now().Format("20060102-150405")+"-"+m.ID[:8])
	if err = publishTree(ctx, stage, output); err != nil {
		return result, fmt.Errorf("发布备份目录失败: %w", err)
	}
	stage = ""
	result.Path = output
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
	for _, c := range path {
		if c < 32 {
			return false
		}
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
	root, err := media.Canonical(path)
	if err != nil {
		return Manifest{}, nil, err
	}
	m, err := readMetadata(ctx, root)
	if err != nil {
		return m, nil, err
	}
	expected, err := expectedTree(m)
	if err != nil {
		return m, nil, err
	}
	if _, _, err = inspectTree(ctx, root, expected, false); err != nil {
		return m, nil, err
	}
	items := []media.Item{}
	buffer := make([]byte, 1024*1024)
	var bytes int64
	for _, e := range m.Entries {
		if err = ctx.Err(); err != nil {
			return m, nil, err
		}
		if e.Excluded != "" {
			continue
		}
		rel, er := PayloadPath(m, e)
		if er != nil {
			return m, nil, er
		}
		physical, er := verifyFile(ctx, root, rel, e.SHA256, e.Item.Bytes, buffer)
		if er != nil {
			return m, nil, er
		}
		item := e.Item
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
		bytes += item.Bytes
		if progress != nil {
			progress(len(items), bytes)
		}
	}
	reread, err := readMetadata(ctx, root)
	if err != nil {
		return m, nil, err
	}
	if !reflect.DeepEqual(m.head(), reread.head()) {
		return m, nil, errors.New("备份元数据在校验时变化")
	}
	if _, _, err = inspectTree(ctx, root, expected, false); err != nil {
		return m, nil, err
	}
	return m, items, nil
}

func remainingStage(path string) string {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return path
	}
	return ""
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
