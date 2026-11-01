package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"
)

type BackupRecord struct {
	SourceCleanupJob    string          `json:"source_cleanup_job,omitempty"`
	SourceCleanupDetail string          `json:"source_cleanup_detail,omitempty"`
	ID                  string          `json:"id"`
	Name                string          `json:"name"`
	Path                string          `json:"path"`
	Format              string          `json:"format"`
	CreatedAt           time.Time       `json:"created_at"`
	Files               int             `json:"files"`
	Bytes               int64           `json:"bytes"`
	Selection           json.RawMessage `json:"selection"`
	Manifest            string          `json:"-"`
	Digest              string          `json:"digest"`
	Status              string          `json:"status"`
	Detail              string          `json:"detail"`
	DeletePath          string          `json:"-"`
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func (c *Catalog) initBackups() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS backup_records(id TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS backup_imports(id TEXT PRIMARY KEY,payload TEXT NOT NULL);`)
	return err
}

type storedBackup struct {
	Record     BackupRecord `json:"record"`
	Manifest   string       `json:"manifest"`
	DeletePath string       `json:"delete_path"`
}

func (c *Catalog) Backup(id string) (BackupRecord, error) {
	var payload string
	err := c.db.QueryRow("SELECT payload FROM backup_records WHERE id=?", id).Scan(&payload)
	var row storedBackup
	if err == nil {
		err = json.Unmarshal([]byte(payload), &row)
	}
	row.Record.Manifest, row.Record.DeletePath = row.Manifest, row.DeletePath
	return row.Record, err
}
func (c *Catalog) Backups() ([]BackupRecord, error) {
	rows, err := c.db.Query("SELECT json_extract(payload,'$.record') FROM backup_records ORDER BY rowid DESC LIMIT 10001")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupRecord{}
	for rows.Next() {
		var payload string
		var row BackupRecord
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(payload), &row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if len(out) > 10000 {
		return nil, errors.New("备份历史超过 10000 条上限")
	}
	return out, rows.Err()
}
func (c *Catalog) SaveBackup(row BackupRecord) error {
	previous, err := c.Backup(row.ID)
	if err == nil && previous.Digest != row.Digest {
		return errors.New("同一备份编号对应不同清单，拒绝替换记录")
	}
	if err == nil && row.SourceCleanupJob == "" {
		row.SourceCleanupJob = previous.SourceCleanupJob
		row.SourceCleanupDetail = previous.SourceCleanupDetail
	}
	if err == nil && row.Manifest == "" {
		row.Manifest = previous.Manifest
	}
	if len(row.Manifest) > 32*1024*1024 || !json.Valid([]byte(row.Manifest)) || ManifestDigest([]byte(row.Manifest)) != row.Digest {
		return errors.New("备份登记清单无效或校验摘要不符")
	}
	if err != nil && !isNoRows(err) {
		return err
	}
	if err != nil {
		if info, e := os.Stat(c.path); e == nil && info.Size()+int64(len(row.Manifest))*2 > 512*1024*1024 {
			return errors.New("索引容量不足以登记此备份，请先备份并维护索引")
		}
		var n int
		if err = c.db.QueryRow("SELECT count(*) FROM backup_records").Scan(&n); err != nil {
			return err
		}
		if n >= 10000 {
			return errors.New("备份历史达到上限")
		}
	}
	b, err := json.Marshal(storedBackup{Record: row, Manifest: row.Manifest, DeletePath: row.DeletePath})
	if err != nil {
		return err
	}
	_, err = c.db.Exec("INSERT INTO backup_records VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", row.ID, string(b))
	return err
}
func ManifestDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (c *Catalog) SaveImport(id string, payload []byte) error {
	if len(payload) > 64*1024*1024 {
		return errors.New("加载结果超过容量上限")
	}
	if info, e := os.Stat(c.path); e == nil && info.Size()+int64(len(payload)) > 512*1024*1024 {
		return errors.New("索引容量不足以保存加载记录；已复制媒体仍保留")
	}
	_, err := c.db.Exec("INSERT INTO backup_imports VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", id, string(payload))
	return err
}
func (c *Catalog) Import(id string) ([]byte, error) {
	var payload string
	err := c.db.QueryRow("SELECT payload FROM backup_imports WHERE id=?", id).Scan(&payload)
	return []byte(payload), err
}
