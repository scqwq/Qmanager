package archive

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/domain"
	"qqarchive/internal/source/plain"
	"strings"
	"time"
)

const DBModel = `CREATE TABLE messages(message_key TEXT PRIMARY KEY,sender TEXT NOT NULL,timestamp TEXT NOT NULL,sequence INTEGER NOT NULL,kind TEXT NOT NULL,text TEXT NOT NULL,raw TEXT NOT NULL);
CREATE INDEX message_order ON messages(timestamp,sequence,message_key);
CREATE TABLE media_objects(sha256 TEXT PRIMARY KEY,bytes INTEGER NOT NULL,path TEXT NOT NULL);
CREATE TABLE media_references(message_key TEXT NOT NULL REFERENCES messages(message_key),segment INTEGER NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,object_sha256 TEXT REFERENCES media_objects(sha256),original_name TEXT NOT NULL,PRIMARY KEY(message_key,segment));`

type Checksum struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type Result struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Messages int64  `json:"messages"`
	Status   string `json:"status"`
}
type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

func (r cancelReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func writeJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(p, b, 0600)
}
func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func Build(ctx context.Context, source string, s domain.Selection, output string) (result Result, err error) {
	if !s.Start.Before(s.End) || s.Conversation == "" {
		return result, errors.New("invalid selection")
	}
	if _, e := time.LoadLocation(s.Timezone); e != nil {
		return result, errors.New("invalid timezone")
	}
	out, e := plain.Canonical(output)
	if e != nil {
		return result, e
	}
	src, e := plain.Canonical(source)
	if e != nil {
		return result, e
	}
	root := filepath.Dir(src)
	rel, e := filepath.Rel(root, out)
	if e != nil || rel == "." || filepath.IsLocal(rel) {
		return result, errors.New("output overlaps source directory")
	}
	if _, e = os.Lstat(out); !os.IsNotExist(e) {
		return result, errors.New("output already exists or inaccessible")
	}
	r, e := plain.Open(ctx, src)
	if e != nil {
		return result, e
	}
	defer r.Close()
	name, kind, e := r.Conversation(ctx, s.Conversation)
	if e != nil {
		return result, e
	}
	scratch, e := os.MkdirTemp(filepath.Dir(out), ".qqarchive-job-")
	if e != nil {
		return result, e
	}
	defer os.RemoveAll(scratch)
	dbPath := filepath.Join(scratch, "messages.db")
	db, e := sql.Open("sqlite", dbPath)
	if e != nil {
		return result, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, DBModel); e != nil {
		return result, e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	m := domain.Manifest{Format: "qqarchive", Major: 1, Minor: 0, ArchiveID: uuid(), Created: time.Now().UTC().Format(time.RFC3339Nano), Exporter: domain.ExporterVersion, Parser: "plain-demo-v1", SourceType: "synthetic_plain", SourceVersion: "unknown", SchemaFingerprint: r.Fingerprint, Account: r.Account, ConversationID: s.Conversation, ConversationName: name, ConversationType: kind, Selection: s, Consistency: "offline-fixture-with-hash-recheck", Integrity: "complete_supported_content", Algorithm: "sha256"}
	e = r.Iterate(ctx, s, func(msg domain.Message) error {
		if _, e := time.Parse(time.RFC3339Nano, msg.Timestamp); e != nil {
			return errors.New("invalid source timestamp")
		}
		if !json.Valid([]byte(msg.Raw)) {
			return errors.New("invalid raw JSON")
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO messages VALUES(?,?,?,?,?,?,?)", msg.Key, msg.Sender, msg.Timestamp, msg.Sequence, msg.Kind, msg.Text, msg.Raw)
		if e == nil {
			m.Messages++
			if msg.Kind == "text" {
				m.Parsed++
			} else {
				m.Unsupported++
			}
		}
		return e
	})
	if e != nil {
		return result, e
	}
	if e = tx.Commit(); e != nil {
		return result, e
	}
	if e = r.Stable(ctx); e != nil {
		return result, e
	}
	var check string
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
		return result, errors.New("generated SQLite integrity check failed")
	}
	if e = db.Close(); e != nil {
		return result, e
	}
	status := "READY"
	if m.Unsupported > 0 {
		status = "READY_WITH_WARNINGS"
		m.Integrity = "has_unsupported_content"
	}
	payload := []string{"manifest.json", "messages.db", "metadata/conversation.json", "metadata/participants.json", "reports/export.json"}
	for _, d := range []string{"metadata", "reports"} {
		if e = os.Mkdir(filepath.Join(scratch, d), 0700); e != nil {
			return result, e
		}
	}
	if e = writeJSON(filepath.Join(scratch, "manifest.json"), m); e != nil {
		return result, e
	}
	if e = writeJSON(filepath.Join(scratch, "metadata", "conversation.json"), map[string]string{"id": m.ConversationID, "name": name, "type": kind}); e != nil {
		return result, e
	}
	// IDs are in messages.db; historical names are unknown and are never invented.
	if e = writeJSON(filepath.Join(scratch, "metadata", "participants.json"), map[string]string{"names": "unknown", "identifiers": "messages.db"}); e != nil {
		return result, e
	}
	if e = writeJSON(filepath.Join(scratch, "reports", "export.json"), map[string]any{"development_demo": true, "status": status, "messages": m.Messages, "unsupported": m.Unsupported, "media_references": 0, "warning": "Plaintext archive. No QQ restoration, cloud verification, or source cleanup authorization."}); e != nil {
		return result, e
	}
	sums := map[string]Checksum{}
	for _, p := range payload {
		f := filepath.Join(scratch, filepath.FromSlash(p))
		h, e := plain.FileHash(ctx, f)
		if e != nil {
			return result, e
		}
		info, e := os.Stat(f)
		if e != nil {
			return result, e
		}
		sums[p] = Checksum{info.Size(), h}
	}
	if e = writeJSON(filepath.Join(scratch, "checksums.json"), sums); e != nil {
		return result, e
	}
	payload = append(payload, "checksums.json")
	partial := filepath.Join(scratch, "archive.partial")
	f, e := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	zw := zip.NewWriter(f)
	for _, p := range payload {
		w, e := zw.CreateHeader(&zip.FileHeader{Name: p, Method: zip.Deflate})
		if e != nil {
			zw.Close()
			f.Close()
			return result, e
		}
		input, e := os.Open(filepath.Join(scratch, filepath.FromSlash(p)))
		if e != nil {
			zw.Close()
			f.Close()
			return result, e
		}
		_, e = io.Copy(w, cancelReader{ctx, input})
		input.Close()
		if e != nil {
			zw.Close()
			f.Close()
			return result, e
		}
	}
	if e = zw.Close(); e != nil {
		f.Close()
		return result, e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return result, e
	}
	if e = f.Close(); e != nil {
		return result, e
	}
	v, e := Open(ctx, partial)
	if e != nil {
		return result, e
	}
	v.Close()
	h, e := plain.FileHash(ctx, partial)
	if e != nil {
		return result, e
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	if e = publishNoReplace(partial, out); e != nil {
		return result, fmt.Errorf("no-overwrite publication failed: %w", e)
	}
	return Result{out, h, m.Messages, status}, nil
}

type Opened struct {
	Manifest domain.Manifest
	db       *sql.DB
	scratch  string
	zip      *zip.ReadCloser
}

func (o *Opened) Close() error { e := o.db.Close(); o.zip.Close(); os.RemoveAll(o.scratch); return e }
func safeName(n string) bool {
	if n == "" || strings.ContainsAny(n, "\\:\x00") || strings.HasPrefix(n, "/") || strings.HasSuffix(n, "/") {
		return false
	}
	for _, part := range strings.Split(n, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		stem := strings.ToUpper(strings.Split(part, ".")[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9' {
			return false
		}
	}
	return true
}
func smallJSON(f *zip.File, v any, max int64) error {
	r, e := f.Open()
	if e != nil {
		return e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, max+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > max {
		return errors.New("metadata limit exceeded")
	}
	return json.Unmarshal(b, v)
}
func Open(ctx context.Context, p string) (out *Opened, err error) {
	z, e := zip.OpenReader(p)
	if e != nil {
		return nil, e
	}
	scratch, e := os.MkdirTemp("", "qqarchive-open-")
	if e != nil {
		z.Close()
		return nil, e
	}
	o := &Opened{zip: z, scratch: scratch}
	defer func() {
		if err != nil {
			if o.db != nil {
				o.db.Close()
			}
			z.Close()
			os.RemoveAll(scratch)
		}
	}()
	if len(z.File) > 10000 {
		return nil, errors.New("entry limit exceeded")
	}
	files := map[string]*zip.File{}
	var total uint64
	for _, f := range z.File {
		if !safeName(f.Name) || f.Mode()&os.ModeType != 0 {
			return nil, errors.New("unsafe archive entry")
		}
		if files[f.Name] != nil {
			return nil, errors.New("duplicate entry")
		}
		files[f.Name] = f
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 512*1024*1024 || total > 1024*1024*1024 {
			return nil, errors.New("prototype expansion limit exceeded")
		}
		if f.UncompressedSize64 > 1024*1024 && f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > 1000 {
			return nil, errors.New("compression ratio limit exceeded")
		}
	}
	required := []string{"manifest.json", "messages.db", "metadata/conversation.json", "metadata/participants.json", "reports/export.json", "checksums.json"}
	// This development profile supports no media yet. Reject extra payloads even
	// if someone recomputed checksums, rather than silently accepting them.
	if len(files) != len(required) {
		return nil, errors.New("unsupported payload entries")
	}
	for _, n := range required {
		if files[n] == nil {
			return nil, errors.New("required entry missing")
		}
	}
	var sums map[string]Checksum
	if e = smallJSON(files["checksums.json"], &sums, 2*1024*1024); e != nil {
		return nil, e
	}
	if len(sums) != len(files)-1 {
		return nil, errors.New("checksum entry set mismatch")
	}
	if _, ok := sums["checksums.json"]; ok {
		return nil, errors.New("recursive checksum rejected")
	}
	for n, f := range files {
		if n == "checksums.json" {
			continue
		}
		sum, ok := sums[n]
		if !ok || sum.Bytes < 0 || uint64(sum.Bytes) != f.UncompressedSize64 {
			return nil, errors.New("checksum entry mismatch")
		}
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		h := sha256.New()
		w := io.Writer(h)
		var output *os.File
		if n == "messages.db" {
			output, e = os.OpenFile(filepath.Join(scratch, "messages.db"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				r.Close()
				return nil, e
			}
			w = io.MultiWriter(h, output)
		}
		count, e := io.Copy(w, cancelReader{ctx, io.LimitReader(r, int64(f.UncompressedSize64)+1)})
		r.Close()
		if output != nil {
			ce := output.Close()
			if e == nil {
				e = ce
			}
		}
		if e != nil {
			return nil, e
		}
		if count != sum.Bytes || hex.EncodeToString(h.Sum(nil)) != sum.SHA256 {
			return nil, errors.New("payload hash mismatch")
		}
	}
	if e = smallJSON(files["manifest.json"], &o.Manifest, 1024*1024); e != nil {
		return nil, e
	}
	m := o.Manifest
	if m.Format != "qqarchive" || m.Major != 1 || m.Algorithm != "sha256" || m.SourceType != "synthetic_plain" || m.ArchiveID == "" {
		return nil, errors.New("unsupported archive format or source")
	}
	if m.Messages < 0 || m.Parsed < 0 || m.Unsupported < 0 || m.Messages != m.Parsed+m.Unsupported || m.MediaReferences != 0 || m.MediaObjects != 0 || m.Missing != 0 {
		return nil, errors.New("manifest counts invalid")
	}
	if !m.Selection.Start.Before(m.Selection.End) || m.Selection.Conversation != m.ConversationID || m.Account == "" || m.ConversationID == "" {
		return nil, errors.New("invalid manifest selection or identity")
	}
	if _, e = time.LoadLocation(m.Selection.Timezone); e != nil {
		return nil, errors.New("invalid manifest timezone")
	}
	if m.Unsupported > 0 && m.Integrity != "has_unsupported_content" || m.Unsupported == 0 && m.Integrity != "complete_supported_content" {
		return nil, errors.New("content status mismatch")
	}
	var conversation struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if e = smallJSON(files["metadata/conversation.json"], &conversation, 1024*1024); e != nil {
		return nil, e
	}
	if conversation.ID != m.ConversationID || conversation.Name != m.ConversationName || conversation.Type != m.ConversationType {
		return nil, errors.New("conversation metadata mismatch")
	}
	o.db, e = plain.ReadOnly(ctx, filepath.Join(scratch, "messages.db"))
	if e != nil {
		return nil, e
	}
	if e = validateDB(ctx, o.db, m); e != nil {
		return nil, e
	}
	return o, nil
}
func validateDB(ctx context.Context, db *sql.DB, m domain.Manifest) error {
	rows, e := db.QueryContext(ctx, "SELECT type,name,coalesce(sql,'') FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'")
	if e != nil {
		return e
	}
	allowed := map[string]bool{"messages": false, "media_objects": false, "media_references": false, "message_order": false}
	for rows.Next() {
		var t, n, s string
		if e = rows.Scan(&t, &n, &s); e != nil {
			rows.Close()
			return e
		}
		expectedType := "table"
		if n == "message_order" {
			expectedType = "index"
		}
		if _, ok := allowed[n]; !ok || t != expectedType || strings.Contains(strings.ToUpper(s), "VIRTUAL") {
			rows.Close()
			return errors.New("unsupported SQLite schema")
		}
		allowed[n] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, ok := range allowed {
		if !ok {
			return errors.New("required SQLite table missing")
		}
	}
	var check string
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
		return errors.New("SQLite integrity failure")
	}
	var count, parsed, unsupported, objects, refs int64
	if e = db.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(kind='text'),0),coalesce(sum(kind!='text'),0) FROM messages").Scan(&count, &parsed, &unsupported); e != nil {
		return e
	}
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM media_objects").Scan(&objects); e != nil {
		return e
	}
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM media_references").Scan(&refs); e != nil {
		return e
	}
	if count != m.Messages || parsed != m.Parsed || unsupported != m.Unsupported || objects != 0 || refs != 0 {
		return errors.New("database counts mismatch")
	}
	// Independently enumerate archived records; do not trust the builder's counts.
	rows, e = db.QueryContext(ctx, "SELECT message_key,sender,timestamp,sequence,raw FROM messages ORDER BY timestamp,sequence,message_key")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var key, sender, stamp, raw string
		var seq int64
		if e = rows.Scan(&key, &sender, &stamp, &seq, &raw); e != nil {
			return e
		}
		ts, e := time.Parse(time.RFC3339Nano, stamp)
		if e != nil || key == "" || sender == "" || !json.Valid([]byte(raw)) || ts.Before(m.Selection.Start) || !ts.Before(m.Selection.End) {
			return errors.New("invalid archived message or range")
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	rows.Close()
	return nil
}
func (o *Opened) Search(ctx context.Context, text string, limit, offset int) ([]domain.Message, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("invalid pagination")
	}
	literal := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(text)
	rows, e := o.db.QueryContext(ctx, `SELECT message_key,sender,timestamp,sequence,kind,text,raw FROM messages WHERE text LIKE ? ESCAPE '\' ORDER BY timestamp,sequence,message_key LIMIT ? OFFSET ?`, "%"+literal+"%", limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []domain.Message{}
	for rows.Next() {
		var m domain.Message
		if e = rows.Scan(&m.Key, &m.Sender, &m.Timestamp, &m.Sequence, &m.Kind, &m.Text, &m.Raw); e != nil {
			return nil, e
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
