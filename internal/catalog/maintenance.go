package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"qqarchive/internal/storage"
	"time"
)

type DatabaseMaintenance struct {
	Before        DatabaseUsage `json:"before"`
	After         DatabaseUsage `json:"after"`
	Previews      int           `json:"previews"`
	Notifications int64         `json:"notifications"`
}

func (c *Catalog) Sweep(ctx context.Context, now time.Time) (DatabaseMaintenance, error) {
	result := DatabaseMaintenance{}
	rows, err := c.db.QueryContext(ctx, "SELECT id FROM cleanup_jobs WHERE state IN ('prepared','interrupted')")
	if err != nil {
		return result, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		job, err := c.Cleanup(id)
		if err != nil {
			return result, err
		}
		if job.Executed || job.Archived || job.ExpiresAt.IsZero() || now.Before(job.ExpiresAt.Add(7*24*time.Hour)) || job.Recycled+job.Failed+job.Uncertain > 0 || job.Ready+job.Pending+job.Skipped != job.Total {
			continue
		}
		oldState := job.State
		job.State, job.Archived = "archived", true
		payload, err := json.Marshal(job)
		if err != nil {
			return result, err
		}
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			return result, err
		}
		changed, err := tx.ExecContext(ctx, "UPDATE cleanup_jobs SET state='archived',payload=? WHERE id=? AND state=? AND json_extract(payload,'$.expires_at')=? AND coalesce(json_extract(payload,'$.executed'),0)=0", string(payload), id, oldState, job.ExpiresAt.Format(time.RFC3339Nano))
		if err == nil {
			var n int64
			n, err = changed.RowsAffected()
			if n > 0 && err == nil {
				_, err = tx.ExecContext(ctx, "DELETE FROM cleanup_items WHERE job=?", id)
				if err == nil {
					_, err = tx.ExecContext(ctx, "UPDATE backup_records SET payload=json_set(payload,'$.record.cleanup_result.state','archived','$.record.source_cleanup_detail','过期预检明细已回收，尚未执行回收') WHERE json_extract(payload,'$.record.source_cleanup_job')=?", id)
				}
				result.Previews++
			}
		}
		if err != nil {
			tx.Rollback()
			return result, err
		}
		if err = tx.Commit(); err != nil {
			return result, err
		}
	}
	deleted, err := c.db.ExecContext(ctx, "DELETE FROM watch_events WHERE processed=1 AND at<?", now.Add(-30*24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return result, err
	}
	result.Notifications, err = deleted.RowsAffected()
	if err == nil {
		var duplicates sql.Result
		duplicates, err = c.db.ExecContext(ctx, "DELETE FROM watch_events WHERE processed=1 AND id NOT IN (SELECT min(id) FROM watch_events WHERE processed=1 GROUP BY source,path,action,at)")
		if err == nil {
			var n int64
			n, err = duplicates.RowsAffected()
			result.Notifications += n
		}
	}
	return result, err
}

func (c *Catalog) Compact(ctx context.Context) (DatabaseMaintenance, error) {
	var result DatabaseMaintenance
	var err error
	result.Before, err = c.Usage(ctx)
	if err != nil {
		return result, err
	}
	free, err := storage.Free(filepath.Dir(c.path))
	if err != nil || free < uint64(result.Before.Bytes*2+(16<<20)) {
		return result, errors.New("整理所需临时空间不足")
	}
	result, err = c.sweepWithUsage(ctx, result.Before)
	if err != nil {
		return result, err
	}
	// References are checked across registry, imports, evidence and every
	// remaining per-file audit. A deleted history row alone cannot free evidence.
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM cleanup_evidence WHERE NOT EXISTS(
SELECT 1 FROM cleanup_items i WHERE json_extract(i.payload,'$.evidence')=cleanup_evidence.digest);
DELETE FROM manifest_snapshots WHERE
NOT EXISTS(SELECT 1 FROM backup_records b WHERE json_extract(b.payload,'$.manifest_ref')=manifest_snapshots.digest) AND
NOT EXISTS(SELECT 1 FROM backup_imports b WHERE json_extract(b.payload,'$.manifest_ref')=manifest_snapshots.digest) AND
NOT EXISTS(SELECT 1 FROM restored_backups b WHERE b.manifest='snapshot:'||manifest_snapshots.digest) AND
NOT EXISTS(SELECT 1 FROM cleanup_items i WHERE json_extract(i.payload,'$.manifest_ref')=manifest_snapshots.digest);`)
	if err != nil {
		tx.Rollback()
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if _, err = c.db.ExecContext(ctx, "VACUUM"); err != nil {
		return result, err
	}
	var integrity string
	if err = c.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return result, err
	}
	if integrity != "ok" {
		return result, errors.New("整理后索引检查未通过，请保留应用记录备份")
	}
	result.After, err = c.Usage(ctx)
	return result, err
}

func (c *Catalog) sweepWithUsage(ctx context.Context, before DatabaseUsage) (DatabaseMaintenance, error) {
	result, err := c.Sweep(ctx, time.Now())
	result.Before = before
	return result, err
}

func (c *Catalog) initNotifications() error {
	rows, err := c.db.Query("PRAGMA table_info(watch_events)")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var def any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "processed" {
			found = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		_, err = c.db.Exec("ALTER TABLE watch_events ADD COLUMN processed INTEGER NOT NULL DEFAULT 0")
	}
	return err
}

func (c *Catalog) EventBoundary(source string) (int64, error) {
	var boundary int64
	err := c.db.QueryRow("SELECT coalesce(max(id),0) FROM watch_events WHERE source=?", source).Scan(&boundary)
	return boundary, err
}

func (c *Catalog) ProcessedEvents(ctx context.Context, source string, boundary int64, covered []string, full bool) error {
	if boundary == 0 {
		return nil
	}
	if full {
		_, err := c.db.ExecContext(ctx, "UPDATE watch_events SET processed=1 WHERE source=? AND id<=?", source, boundary)
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, prefix := range covered {
		_, err = tx.ExecContext(ctx, "UPDATE watch_events SET processed=1 WHERE source=? AND id<=? AND (path=? OR (path>=? AND path<?))", source, boundary, prefix, prefix+"/", prefix+"0")
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *Catalog) migrateOnce() error {
	var complete string
	err := c.db.QueryRow("SELECT value FROM app_preferences WHERE key='evidence_storage_version'").Scan(&complete)
	if err == nil && complete == "1" {
		return nil
	}
	if err != nil && !isNoRows(err) {
		return err
	}
	if err = c.migrateEvidence(); err != nil {
		return err
	}
	_, err = c.db.Exec("UPDATE qman_meta SET version=2; INSERT INTO app_preferences(key,value) VALUES('evidence_storage_version','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
	return err
}

func (c *Catalog) MigrateStorage() error {
	_, err := c.db.Exec("UPDATE qman_meta SET version=2")
	if err == nil {
		err = c.migrateOnce()
	}
	if err == nil {
		c.storageReady.Store(true)
	}
	return err
}

func (c *Catalog) StorageReady() bool { return c.storageReady.Load() }
