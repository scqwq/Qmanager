// Package plain handles only QQArchive synthetic plaintext fixtures.
// It is not an NTQQ adapter and never tries to guess QQ tables.
package plain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"qqarchive/internal/domain"
	"strings"
)

const Schema = `CREATE TABLE source_info(format TEXT NOT NULL, account TEXT NOT NULL);
CREATE TABLE conversations(id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL);
CREATE TABLE messages(id TEXT PRIMARY KEY, conversation TEXT NOT NULL REFERENCES conversations(id), sender TEXT NOT NULL, timestamp TEXT NOT NULL, sequence INTEGER NOT NULL, kind TEXT NOT NULL, text TEXT NOT NULL, raw TEXT NOT NULL);
CREATE INDEX messages_order ON messages(conversation,timestamp,sequence,id);`

type Reader struct {
	DB                               *sql.DB
	Path, Hash, Account, Fingerprint string
}

func FileHash(ctx context.Context, p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	b := make([]byte, 1024*1024)
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, err := f.Read(b)
		if n > 0 {
			h.Write(b[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Reject any path with a reparse/symlink alias instead of assuming lexical isolation.
func Canonical(p string) (string, error) {
	a, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	for c := a; ; c = filepath.Dir(c) {
		if _, e := os.Lstat(c); e == nil {
			r, e := filepath.EvalSymlinks(c)
			if e != nil {
				return "", e
			}
			if !strings.EqualFold(filepath.Clean(r), filepath.Clean(c)) {
				return "", errors.New("linked path rejected")
			}
		} else if !os.IsNotExist(e) {
			return "", e
		}
		if filepath.Dir(c) == c {
			break
		}
	}
	return a, nil
}
func ReadOnly(ctx context.Context, p string) (*sql.DB, error) {
	a, e := Canonical(p)
	if e != nil {
		return nil, e
	}
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		if _, e := os.Stat(a + s); e == nil {
			return nil, errors.New("database sidecars require a verified snapshot")
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	uriPath := filepath.ToSlash(a)
	if filepath.VolumeName(a) != "" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "trusted_schema(0)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func Open(ctx context.Context, p string) (*Reader, error) {
	hash, e := FileHash(ctx, p)
	if e != nil {
		return nil, e
	}
	db, e := ReadOnly(ctx, p)
	if e != nil {
		return nil, e
	}
	r := &Reader{DB: db, Path: p, Hash: hash}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	var format string
	if e = db.QueryRowContext(ctx, "SELECT format,account FROM source_info").Scan(&format, &r.Account); e != nil {
		return nil, errors.New("unsupported plain source schema")
	}
	if format != "qqarchive-synthetic-v1" {
		return nil, errors.New("not a synthetic source")
	}
	rows, e := db.QueryContext(ctx, "SELECT type,name,coalesce(sql,'') FROM sqlite_schema ORDER BY type,name")
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	for rows.Next() {
		var t, n, s string
		if e = rows.Scan(&t, &n, &s); e != nil {
			rows.Close()
			return nil, e
		}
		if t == "view" || t == "trigger" {
			rows.Close()
			return nil, errors.New("executable schema rejected")
		}
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", t, n, s)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	r.Fingerprint = hex.EncodeToString(h.Sum(nil))
	ok = true
	return r, nil
}
func (r *Reader) Close() error { return r.DB.Close() }
func (r *Reader) Stable(ctx context.Context) error {
	h, e := FileHash(ctx, r.Path)
	if e != nil {
		return e
	}
	if h != r.Hash {
		return errors.New("source changed")
	}
	return nil
}
func (r *Reader) Conversation(ctx context.Context, id string) (name, kind string, err error) {
	err = r.DB.QueryRowContext(ctx, "SELECT name,kind FROM conversations WHERE id=?", id).Scan(&name, &kind)
	return
}
func (r *Reader) Iterate(ctx context.Context, s domain.Selection, emit func(domain.Message) error) error {
	rows, e := r.DB.QueryContext(ctx, "SELECT id,sender,timestamp,sequence,kind,text,raw FROM messages WHERE conversation=? AND timestamp>=? AND timestamp<? ORDER BY timestamp,sequence,id", s.Conversation, s.Start.Format("2006-01-02T15:04:05.000000000Z"), s.End.Format("2006-01-02T15:04:05.000000000Z"))
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var m domain.Message
		if e = rows.Scan(&m.Key, &m.Sender, &m.Timestamp, &m.Sequence, &m.Kind, &m.Text, &m.Raw); e != nil {
			return e
		}
		if e = emit(m); e != nil {
			return e
		}
	}
	return rows.Err()
}
func Demo(ctx context.Context, dir string) error {
	a, e := Canonical(dir)
	if e != nil {
		return e
	}
	if e = os.Mkdir(a, 0700); e != nil {
		return e
	}
	db, e := sql.Open("sqlite", filepath.Join(a, "source.db"))
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, Schema); e != nil {
		return e
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO source_info VALUES('qqarchive-synthetic-v1','demo-account'); INSERT INTO conversations VALUES('demo-chat','开发演示会话','private');"); e != nil {
		return e
	}
	texts := []string{"你好，这是开发演示。", "重复文本", "重复文本", "<script>alert('test')</script>", "未知消息占位"}
	for i, t := range texts {
		kind := "text"
		if i == 4 {
			kind = "unknown"
		}
		raw, _ := json.Marshal(map[string]any{"synthetic": true, "kind": kind, "text": t})
		if _, e = tx.ExecContext(ctx, "INSERT INTO messages VALUES(?,?,?,?,?,?,?,?)", fmt.Sprintf("demo-%d", i), "demo-chat", "900719925474099312345", fmt.Sprintf("2026-10-03T01:00:0%d.000000000Z", i), i, kind, t, string(raw)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
