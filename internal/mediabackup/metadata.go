package mediabackup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"strings"
	"time"
)

const Format = "qman-media-tree"
const Version = 2
const headerName = "backup.json"
const indexName = ".qman/files.jsonl"
const maxHeader = 64 * 1024
const maxIndex = 128 * 1024 * 1024
const maxLine = 1024 * 1024
const maxDirectories = 50000

type header struct {
	Format        string       `json:"format"`
	Version       int          `json:"version"`
	ID            string       `json:"id"`
	CreatedAt     time.Time    `json:"created_at"`
	SourceID      string       `json:"source_id"`
	SourceName    string       `json:"source_name"`
	RestorePrefix string       `json:"restore_prefix,omitempty"`
	PayloadRoot   string       `json:"payload_root"`
	Selection     media.Filter `json:"selection"`
	IndexSHA256   string       `json:"index_sha256"`
	Entries       int          `json:"entries"`
	Files         int          `json:"files"`
	Bytes         int64        `json:"bytes"`
}

// Each directory path is written once. Times are UTC RFC3339Nano in order:
// modified, created, first seen, last seen, last changed, stable observed.
// An empty time means unknown, never an inferred download timestamp.
type compactFile struct {
	Name       string      `json:"n"`
	Instance   string      `json:"i"`
	Identity   string      `json:"v,omitempty"`
	Bytes      int64       `json:"z"`
	SHA256     string      `json:"h,omitempty"`
	Kind       string      `json:"k"`
	Month      string      `json:"m,omitempty"`
	Role       string      `json:"r"`
	Format     string      `json:"f"`
	Status     string      `json:"s,omitempty"`
	Method     string      `json:"o,omitempty"`
	Confidence string      `json:"c,omitempty"`
	Coverage   string      `json:"g,omitempty"`
	Stability  string      `json:"b,omitempty"`
	Times      [6]string   `json:"t"`
	History    [][3]string `json:"e,omitempty"`
	Excluded   string      `json:"x,omitempty"`
}
type indexRow struct {
	Dir       int          `json:"d"`
	Directory *string      `json:"directory,omitempty"`
	File      *compactFile `json:"file,omitempty"`
}
type snapshot struct {
	Header  header     `json:"header"`
	Records []indexRow `json:"records"`
}

func (m Manifest) head() header {
	h := header{Format: m.Format, Version: m.Version, ID: m.ID, CreatedAt: m.CreatedAt, SourceID: m.SourceID, SourceName: m.SourceName, RestorePrefix: m.RestorePrefix, PayloadRoot: m.PayloadRoot, Selection: m.Selection, IndexSHA256: m.IndexSHA256, Entries: len(m.Entries)}
	for _, e := range m.Entries {
		if e.Excluded == "" {
			h.Files++
			h.Bytes += e.Item.Bytes
		}
	}
	return h
}
func fromHeader(h header) Manifest {
	return Manifest{Format: h.Format, Version: h.Version, ID: h.ID, CreatedAt: h.CreatedAt, SourceID: h.SourceID, SourceName: h.SourceName, RestorePrefix: h.RestorePrefix, PayloadRoot: h.PayloadRoot, Selection: h.Selection, IndexSHA256: h.IndexSHA256}
}
func utc(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func timePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func compact(e Entry) compactFile {
	i := e.Item
	f := compactFile{Name: filepath.Base(filepath.FromSlash(i.RelativePath)), Instance: i.InstanceID, Identity: i.Identity, Bytes: i.Bytes, SHA256: e.SHA256, Kind: i.Kind, Month: i.SourceMonth, Role: i.Role, Format: i.Format, Status: i.Status, Method: i.Method, Confidence: i.Confidence, Coverage: i.Coverage, Stability: i.Stability, Excluded: e.Excluded, Times: [6]string{utc(i.ModifiedAt), timePtr(i.CreatedAt), utc(i.FirstSeenAt), utc(i.LastSeenAt), utc(i.LastChangedAt), timePtr(i.StableObservedAt)}}
	if e.Excluded == "" && f.Status == "recognized" {
		f.Status = ""
	}
	if e.Excluded == "" && f.Stability == "stable_heuristic" {
		f.Stability = ""
	}
	for _, h := range e.History {
		f.History = append(f.History, [3]string{h.At, h.Kind, h.Detail})
	}
	return f
}
func expand(dir string, f compactFile) (Entry, error) {
	var times [6]time.Time
	for n, v := range f.Times {
		if v != "" {
			t, e := time.Parse(time.RFC3339Nano, v)
			if e != nil {
				return Entry{}, errors.New("文件时间证据格式错误")
			}
			times[n] = t.UTC()
		}
	}
	if !safeRelative(f.Name) || strings.Contains(f.Name, "/") {
		return Entry{}, errors.New("索引文件名无效")
	}
	path := f.Name
	if dir != "." {
		path = dir + "/" + path
	}
	i := media.Item{ID: f.Instance, InstanceID: f.Instance, Identity: f.Identity, RelativePath: path, Bytes: f.Bytes, Kind: f.Kind, SourceMonth: f.Month, Role: f.Role, Format: f.Format, Status: f.Status, Method: f.Method, Confidence: f.Confidence, Coverage: f.Coverage, Stability: f.Stability, ModifiedAt: times[0], FirstSeenAt: times[2], LastSeenAt: times[3], LastChangedAt: times[4]}
	if f.Times[1] != "" {
		t := times[1]
		i.CreatedAt = &t
	}
	if f.Times[5] != "" {
		t := times[5]
		i.StableObservedAt = &t
	}
	if f.Excluded == "" && i.Status == "" {
		i.Status = "recognized"
	}
	if f.Excluded == "" && i.Stability == "" {
		i.Stability = "stable_heuristic"
	}
	e := Entry{Item: i, SHA256: f.SHA256, Excluded: f.Excluded, History: []catalog.History{}}
	if len(f.History) > 1000 {
		return e, errors.New("文件观察历史超过上限")
	}
	for _, h := range f.History {
		if len(h[1]) > 80 || len(h[2]) > 8192 {
			return e, errors.New("文件观察历史过大")
		}
		if _, err := time.Parse(time.RFC3339Nano, h[0]); err != nil {
			return e, errors.New("历史时间错误")
		}
		e.History = append(e.History, catalog.History{At: h[0], Kind: h[1], Detail: h[2]})
	}
	return e, nil
}
func rowsOf(m Manifest, emit func(indexRow) error) error {
	dirs := map[string]int{}
	for _, e := range m.Entries {
		d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(e.Item.RelativePath)))
		id, ok := dirs[d]
		if !ok {
			id = len(dirs)
			if id >= maxDirectories {
				return errors.New("备份目录数超过上限")
			}
			dirs[d] = id
			p := d
			if err := emit(indexRow{Dir: id, Directory: &p}); err != nil {
				return err
			}
		}
		f := compact(e)
		if err := emit(indexRow{Dir: id, File: &f}); err != nil {
			return err
		}
	}
	return nil
}

type rowReader struct {
	dirs           []string
	directoryNames map[string]bool
	used           []bool
	names          map[string]bool
	instances      map[string]bool
	m              Manifest
}

func newRows(h header) *rowReader {
	return &rowReader{m: fromHeader(h), directoryNames: map[string]bool{}, names: map[string]bool{}, instances: map[string]bool{}}
}
func (r *rowReader) add(row indexRow) error {
	if (row.Directory == nil) == (row.File == nil) {
		return errors.New("索引记录类型无效")
	}
	if row.Directory != nil {
		d := *row.Directory
		if row.Dir != len(r.dirs) || len(r.dirs) >= maxDirectories || (d != "." && !safeRelative(d)) {
			return errors.New("索引目录定义无效")
		}
		key := strings.ToLower(d)
		if r.directoryNames[key] {
			return errors.New("索引目录重复")
		}
		r.directoryNames[key] = true
		r.dirs = append(r.dirs, d)
		r.used = append(r.used, false)
		return nil
	}
	if row.Dir < 0 || row.Dir >= len(r.dirs) || len(r.m.Entries) >= media.MaxFiles {
		return errors.New("文件目录引用或数量无效")
	}
	e, err := expand(r.dirs[row.Dir], *row.File)
	if err != nil {
		return err
	}
	key := strings.ToLower(e.Item.RelativePath)
	if r.names[key] || r.instances[e.Item.InstanceID] {
		return errors.New("重复文件路径或实例")
	}
	r.names[key] = true
	r.used[row.Dir] = true
	r.instances[e.Item.InstanceID] = true
	r.m.Entries = append(r.m.Entries, e)
	return nil
}

func decodeStrict(b []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("元数据含额外JSON内容")
	}
	return nil
}
func (r *rowReader) complete(h header) error {
	for _, used := range r.used {
		if !used {
			return errors.New("索引含未使用目录")
		}
	}
	return validEntries(r.m, h)
}

// Compact snapshots in the private catalogue preserve source-cleanup evidence
// even after the external package has been deleted. This is not the disk header.
func (m Manifest) MarshalJSON() ([]byte, error) {
	s := snapshot{Header: m.head(), Records: []indexRow{}}
	if err := rowsOf(m, func(r indexRow) error { s.Records = append(s.Records, r); return nil }); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}
func (m *Manifest) UnmarshalJSON(b []byte) error {
	if len(b) > maxIndex {
		return errors.New("备份证据超过上限")
	}
	var s snapshot
	if err := decodeStrict(b, &s); err != nil {
		return err
	}
	if err := validHeader(s.Header); err != nil {
		return err
	}
	r := newRows(s.Header)
	for _, row := range s.Records {
		if err := r.add(row); err != nil {
			return err
		}
	}
	if err := r.complete(s.Header); err != nil {
		return err
	}
	*m = r.m
	return nil
}
func validHeader(h header) error {
	if h.Format != Format || h.Version != Version || !safeID.MatchString(h.ID) || h.Entries < 1 || h.Entries > media.MaxFiles || h.Files < 1 || h.Files > h.Entries || h.Bytes < 0 || h.Bytes > maxBytes || !hashName.MatchString(h.IndexSHA256) {
		return errors.New("不支持的备份格式/版本或数量；请使用新版目录树备份")
	}
	if h.PayloadRoot == "Tencent Files" {
		if !validPrefix(h.RestorePrefix) {
			return errors.New("账号路径映射无效")
		}
	} else {
		allowed := map[string]bool{"Media": true, "Media/Pic": true, "Media/Video": true, "Media/Ptt": true, "Media/Emoji/emoji-recv": true}
		if !allowed[h.PayloadRoot] || h.RestorePrefix != "" {
			return errors.New("独立媒体路径无效")
		}
	}
	return media.ValidateFilter(h.Selection)
}
func PayloadPath(m Manifest, e Entry) (string, error) {
	p := m.PayloadRoot + "/" + e.Item.RelativePath
	if m.PayloadRoot == "Tencent Files" {
		mapped, err := mappedPath(m, e)
		if err != nil {
			return "", err
		}
		p = "Tencent Files/" + mapped
	}
	if !safeRelative(p) {
		return "", errors.New("媒体树路径无效")
	}
	return p, nil
}
func validEntries(m Manifest, h header) error {
	if len(m.Entries) != h.Entries {
		return errors.New("索引条目数不符")
	}
	files := 0
	var bytes int64
	for _, e := range m.Entries {
		i := e.Item
		if !safeRelative(i.RelativePath) || !safeID.MatchString(i.InstanceID) || i.Bytes < 0 || i.Bytes > maxBytes {
			return errors.New("索引路径、实例或大小无效")
		}
		if _, err := PayloadPath(m, e); err != nil {
			return err
		}
		if e.Excluded != "" {
			if e.SHA256 != "" {
				return errors.New("排除项不能引用载荷")
			}
			continue
		}
		if !hashName.MatchString(e.SHA256) || reason(i) != "" {
			return errors.New("载荷摘要或备份资格无效")
		}
		files++
		bytes += i.Bytes
		if bytes > maxBytes {
			return errors.New("载荷总量超限")
		}
	}
	if files != h.Files || bytes != h.Bytes {
		return errors.New("载荷数量/长度与备份说明不符")
	}
	return nil
}
func readSmall(ctx context.Context, root, name string, limit int64) ([]byte, error) {
	f, err := media.OpenRelative(root, filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("元数据非普通文件或超过上限")
	}
	b, err := io.ReadAll(io.LimitReader(media.CancelReader{Ctx: ctx, Reader: f}, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("元数据过大")
	}
	return b, err
}
func writeMetadata(ctx context.Context, root string, m *Manifest) error {
	if err := os.Mkdir(filepath.Join(root, ".qman"), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(root, filepath.FromSlash(indexName)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	var total int
	err = rowsOf(*m, func(row indexRow) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, e := json.Marshal(row)
		if e != nil {
			return e
		}
		total += len(b) + 1
		if len(b) > maxLine || total > maxIndex {
			return errors.New("逐文件索引超过行或总量限制，请缩小备份范围")
		}
		b = append(b, '\n')
		_, e = io.MultiWriter(out, hash).Write(b)
		return e
	})
	if err == nil {
		err = out.Sync()
	}
	ce := out.Close()
	if err != nil {
		return err
	}
	if ce != nil {
		return ce
	}
	m.IndexSHA256 = hex.EncodeToString(hash.Sum(nil))
	h := m.head()
	if err = validHeader(h); err != nil {
		return err
	}
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil || len(b) > maxHeader {
		return errors.New("备份说明超过限制")
	}
	f, err := os.OpenFile(filepath.Join(root, headerName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func readMetadata(ctx context.Context, root string) (Manifest, error) {
	b, err := readSmall(ctx, root, headerName, maxHeader)
	if err != nil {
		return Manifest{}, err
	}
	var h header
	if err = decodeStrict(b, &h); err != nil {
		return Manifest{}, err
	}
	if err = validHeader(h); err != nil {
		return Manifest{}, err
	}
	f, err := media.OpenRelative(root, filepath.FromSlash(indexName))
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxIndex {
		return Manifest{}, errors.New("文件索引超过限制或不可读")
	}
	hash := sha256.New()
	r := newRows(h)
	scan := bufio.NewScanner(io.TeeReader(io.LimitReader(media.CancelReader{Ctx: ctx, Reader: f}, maxIndex+1), hash))
	scan.Buffer(make([]byte, 64*1024), maxLine+1)
	for scan.Scan() {
		if err = ctx.Err(); err != nil {
			return Manifest{}, err
		}
		var row indexRow
		if err = decodeStrict(scan.Bytes(), &row); err != nil {
			return Manifest{}, err
		}
		if err = r.add(row); err != nil {
			return Manifest{}, err
		}
	}
	if err = scan.Err(); err != nil {
		return Manifest{}, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != h.IndexSHA256 {
		return Manifest{}, errors.New("逐文件索引SHA-256不符")
	}
	if err = r.complete(h); err != nil {
		return Manifest{}, err
	}
	return r.m, nil
}
