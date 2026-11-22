package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

type BackupPackageCheck struct {
	Path       string    `json:"path"`
	DeletePath string    `json:"delete_path,omitempty"`
	State      string    `json:"state"`
	Detail     string    `json:"detail"`
	At         time.Time `json:"at"`
}

type BackupRecord struct {
	PackageCheck        *BackupPackageCheck  `json:"-"`
	SelectedOnly        bool                 `json:"selected_only,omitempty"`
	Display             BackupDisplay        `json:"display"`
	HasLoaded           bool                 `json:"has_loaded"`
	OriginalFiles       *BackupFileStatus    `json:"original_files,omitempty"`
	CleanupResult       *BackupCleanupStatus `json:"cleanup_result,omitempty"`
	RestoreResult       *BackupRestoreStatus `json:"restore_result,omitempty"`
	LocalState          string               `json:"local_state,omitempty"`
	LocalDetail         string               `json:"local_detail,omitempty"`
	LocationCheckedAt   *time.Time           `json:"location_checked_at,omitempty"`
	SourceCleanupJob    string               `json:"source_cleanup_job,omitempty"`
	SourceCleanupDetail string               `json:"source_cleanup_detail,omitempty"`
	ID                  string               `json:"id"`
	Name                string               `json:"name"`
	Path                string               `json:"path"`
	Format              string               `json:"format"`
	CreatedAt           time.Time            `json:"created_at"`
	Files               int                  `json:"files"`
	Bytes               int64                `json:"bytes"`
	Selection           json.RawMessage      `json:"selection"`
	Manifest            string               `json:"-"`
	Digest              string               `json:"digest"`
	Status              string               `json:"status"`
	Detail              string               `json:"detail"`
	DeletePath          string               `json:"-"`
}

type BackupFileStatus struct {
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
	Stale     bool      `json:"stale"`
	Total     int       `json:"total"`
	Present   int       `json:"present"`
	Missing   int       `json:"missing"`
	Changed   int       `json:"changed"`
	Unknown   int       `json:"unknown"`
}
type BackupCleanupStatus struct {
	Missing     int       `json:"missing"`
	Unavailable int       `json:"unavailable"`
	State       string    `json:"state"`
	At          time.Time `json:"at"`
	Joint       bool      `json:"joint"`
	Recycled    int       `json:"recycled"`
	Skipped     int       `json:"skipped"`
	Failed      int       `json:"failed"`
	Uncertain   int       `json:"uncertain"`
	Remaining   int       `json:"remaining"`
}
type BackupRestoreStatus struct {
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Target     string     `json:"target,omitempty"`
	Written    int        `json:"written"`
	Skipped    int        `json:"skipped"`
	Conflicts  int        `json:"conflicts"`
	Fallback   int        `json:"fallback"`
	Failed     int        `json:"failed"`
	Detail     string     `json:"detail,omitempty"`
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
func (c *Catalog) initBackups() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS backup_records(id TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS backup_imports(id TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS backup_display(id TEXT PRIMARY KEY,digest TEXT NOT NULL,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS backup_events(seq INTEGER PRIMARY KEY,backup_id TEXT NOT NULL,digest TEXT NOT NULL,at INTEGER NOT NULL,action TEXT NOT NULL,detail TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS backup_events_by_backup ON backup_events(backup_id,digest,at DESC,seq DESC);`)
	return err
}

type storedBackup struct {
	PackageCheck *BackupPackageCheck `json:"package_check,omitempty"`
	Record       BackupRecord        `json:"record"`
	Manifest     string              `json:"manifest"`
	ManifestRef  string              `json:"manifest_ref,omitempty"`
	DeletePath   string              `json:"delete_path"`
}

func (c *Catalog) Backup(id string) (BackupRecord, error) {
	var payload string
	err := c.db.QueryRow("SELECT payload FROM backup_records WHERE id=?", id).Scan(&payload)
	var row storedBackup
	if err == nil {
		err = json.Unmarshal([]byte(payload), &row)
	}
	row.Record.Manifest, row.Record.DeletePath, row.Record.PackageCheck = row.Manifest, row.DeletePath, row.PackageCheck
	if err == nil && row.ManifestRef != "" {
		row.Record.Manifest, err = c.snapshot(row.ManifestRef)
		if err == nil && ManifestDigest([]byte(row.Record.Manifest)) != row.Record.Digest {
			err = errors.New("备份登记与固定清单不符")
		}
	}
	return row.Record, err
}
func (c *Catalog) Backups() ([]BackupRecord, error) {
	rows, err := c.db.Query(`SELECT json_extract(r.payload,'$.record'),
        COALESCE(json_extract(r.payload,'$.delete_path'),''),COALESCE(json_extract(r.payload,'$.package_check'),'null'),
		EXISTS(SELECT 1 FROM backup_imports WHERE backup_imports.id=r.id),COALESCE(d.payload,'{}')
		FROM backup_records r LEFT JOIN backup_display d ON d.id=r.id AND d.digest=json_extract(r.payload,'$.record.digest')
		ORDER BY r.rowid DESC LIMIT 10001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupRecord{}
	for rows.Next() {
		var payload, display, deletePath, check string
		var row BackupRecord
		var imported bool
		if err = rows.Scan(&payload, &deletePath, &check, &imported, &display); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(payload), &row); err != nil {
			return nil, err
		}
		row.DeletePath = deletePath
		if err = json.Unmarshal([]byte(check), &row.PackageCheck); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(display), &row.Display); err != nil {
			return nil, err
		}
		if imported && row.RestoreResult == nil {
			row.RestoreResult = &BackupRestoreStatus{State: "legacy", Detail: "已有加载记录；可核对原文件读取旧结果，未重新验证落盘副本"}
		}
		row.HasLoaded = imported
		out = append(out, row)
	}
	if len(out) > 10000 {
		return nil, errors.New("备份历史超过 10000 条上限")
	}
	return out, rows.Err()
}
func (c *Catalog) SaveBackup(row BackupRecord) error {
	// Location observations are a live UI projection, not durable proof of
	// deletion, package integrity, or cloud verification.
	row.LocalState, row.LocalDetail, row.LocationCheckedAt = "", "", nil
	row.Display = BackupDisplay{}
	row.HasLoaded = false
	previous, err := c.Backup(row.ID)
	if err == nil && previous.Digest != row.Digest {
		return errors.New("同一备份编号对应不同清单，拒绝替换记录")
	}
	if err == nil && row.SourceCleanupJob == "" {
		row.SourceCleanupJob = previous.SourceCleanupJob
		row.SourceCleanupDetail = previous.SourceCleanupDetail
	}
	if err == nil {
		row.SelectedOnly = row.SelectedOnly || previous.SelectedOnly
		if row.OriginalFiles == nil {
			row.OriginalFiles = previous.OriginalFiles
		}
		if row.CleanupResult == nil {
			row.CleanupResult = previous.CleanupResult
		}
		if row.RestoreResult == nil || row.RestoreResult.State == "legacy" {
			row.RestoreResult = previous.RestoreResult
		}
	}
	if err == nil && row.Manifest == "" {
		row.Manifest = previous.Manifest
	}
	if len(row.Manifest) > 128*1024*1024 || !json.Valid([]byte(row.Manifest)) || ManifestDigest([]byte(row.Manifest)) != row.Digest {
		return errors.New("备份登记清单无效或校验摘要不符")
	}
	if err != nil && !isNoRows(err) {
		return err
	}
	newRecord := isNoRows(err)
	if err != nil {
		var n int
		if err = c.db.QueryRow("SELECT count(*) FROM backup_records").Scan(&n); err != nil {
			return err
		}
		if n >= 10000 {
			return errors.New("备份历史达到上限")
		}
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ref, err := storeManifest(context.Background(), tx, row.ID, []byte(row.Manifest))
	if err != nil {
		return err
	}
	b, err := json.Marshal(storedBackup{Record: row, ManifestRef: ref, DeletePath: row.DeletePath, PackageCheck: row.PackageCheck})
	if err != nil {
		return err
	}
	if newRecord {
		if err = checkCapacity(context.Background(), tx, int64(len(b))+4096); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO backup_records VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", row.ID, string(b)); err != nil {
		return err
	}
	return tx.Commit()
}
func ManifestDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// DeleteBackupHistory removes only the selected registry row. Imports, restored
// evidence and cleanup audits are independent records and must remain intact.
func (c *Catalog) DeleteBackupHistory(ctx context.Context, id, digest string) ([]string, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var payload string
	if err = tx.QueryRowContext(ctx, "SELECT payload FROM backup_records WHERE id=?", id).Scan(&payload); err != nil {
		return nil, err
	}
	var row storedBackup
	if err = json.Unmarshal([]byte(payload), &row); err != nil {
		return nil, err
	}
	if row.Record.Status != "deleted" || row.DeletePath != "" || row.Record.Digest != digest {
		return nil, errors.New("请先完成本地备份包删除，再删除历史记录")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,state FROM cleanup_jobs WHERE state IN ('preparing','prepared','running')
AND EXISTS (SELECT 1 FROM json_each(cleanup_jobs.payload,'$.backup_ids') WHERE value=?)`, id)
	if err != nil {
		return nil, err
	}
	invalidated := []string{}
	for rows.Next() {
		var job, state string
		if err = rows.Scan(&job, &state); err != nil {
			rows.Close()
			return nil, err
		}
		if state != "prepared" {
			rows.Close()
			return nil, errors.New("关联原文件清理任务仍在进行，请等待任务结束")
		}
		invalidated = append(invalidated, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, job := range invalidated {
		if _, err = tx.ExecContext(ctx, "UPDATE cleanup_jobs SET state='interrupted' WHERE id=?", job); err != nil {
			return nil, err
		}
		// A prepared job may combine several backups. Keep their latest result
		// accurate while revoking this shared, unexecuted confirmation.
		if _, err = tx.ExecContext(ctx, `UPDATE backup_records SET payload=json_set(
CASE WHEN json_type(payload,'$.record.cleanup_result')='object' THEN json_set(payload,'$.record.cleanup_result.state','interrupted') ELSE payload END,
'$.record.source_cleanup_detail',?)
WHERE json_extract(payload,'$.record.source_cleanup_job')=?`, "关联预检因删除备份历史而失效，尚未执行；如需清理请重新预检", job); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM backup_records WHERE id=?", id); err != nil {
		return nil, err
	}
	return invalidated, tx.Commit()
}
func (c *Catalog) SaveImport(id string, payload []byte) error {
	return c.saveImport(id, payload, false)
}

func (c *Catalog) saveImport(id string, payload []byte, migration bool) error {
	originalPayload := string(payload)
	if len(payload) > 192*1024*1024 {
		return errors.New("加载结果超过容量上限")
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(payload, &data); err != nil {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if manifest, ok := data["manifest"]; ok && string(manifest) != "null" {
		var ref string
		var e error
		if migration {
			ref, e = storeManifestMigration(tx, id, manifest)
		} else {
			ref, e = storeManifest(context.Background(), tx, id, manifest)
		}
		if e != nil {
			return e
		}
		data["manifest_ref"], _ = json.Marshal(ref)
		delete(data, "manifest")
		payload, e = json.Marshal(data)
		if e != nil {
			return e
		}
	}
	var old int64
	err = tx.QueryRow("SELECT length(CAST(payload AS BLOB)) FROM backup_imports WHERE id=?", id).Scan(&old)
	if err != nil && !isNoRows(err) {
		return err
	}
	if extra := int64(len(payload)) - old; extra > 0 && !migration {
		if err = checkCapacity(context.Background(), tx, extra+4096); err != nil {
			return err
		}
	}
	if migration {
		_, err = tx.Exec("UPDATE backup_imports SET payload=? WHERE id=? AND payload=?", string(payload), id, originalPayload)
	} else {
		_, err = tx.Exec("INSERT INTO backup_imports VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", id, string(payload))
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (c *Catalog) Import(id string) ([]byte, error) {
	var payload string
	err := c.db.QueryRow("SELECT payload FROM backup_imports WHERE id=?", id).Scan(&payload)
	if err != nil {
		return nil, err
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal([]byte(payload), &data); err != nil {
		return nil, err
	}
	if value, ok := data["manifest_ref"]; ok {
		var ref string
		if err = json.Unmarshal(value, &ref); err != nil {
			return nil, err
		}
		manifest, e := c.snapshot(ref)
		if e != nil {
			return nil, e
		}
		data["manifest"] = json.RawMessage(manifest)
		delete(data, "manifest_ref")
		return json.Marshal(data)
	}
	return []byte(payload), nil
}
