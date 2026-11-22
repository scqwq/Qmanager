package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type DatabaseUsage struct {
	Bytes            int64 `json:"bytes"`
	Used             int64 `json:"used"`
	Reusable         int64 `json:"reusable"`
	Limit            int64 `json:"limit"`
	TransactionBytes int64 `json:"transaction_bytes"`
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func databaseUsage(ctx context.Context, query rowQuery) (DatabaseUsage, error) {
	var pages, size, free int64
	for _, entry := range []struct {
		sql   string
		value *int64
	}{
		{"PRAGMA page_count", &pages}, {"PRAGMA page_size", &size}, {"PRAGMA freelist_count", &free},
	} {
		if err := query.QueryRowContext(ctx, entry.sql).Scan(entry.value); err != nil {
			return DatabaseUsage{}, err
		}
	}
	return DatabaseUsage{Bytes: pages * size, Used: (pages - free) * size, Reusable: free * size, Limit: maxIndexBytes}, nil
}

func checkCapacity(ctx context.Context, query rowQuery, additional int64) error {
	usage, err := databaseUsage(ctx, query)
	if err != nil {
		return err
	}
	if max(0, additional) > maxIndexBytes-usage.Used {
		return errors.New("索引容量不足，请备份应用记录后整理空间")
	}
	return nil
}

func (c *Catalog) Usage(ctx context.Context) (DatabaseUsage, error) {
	usage, err := databaseUsage(ctx, c.db)
	for _, suffix := range []string{"-journal", "-wal"} {
		if info, e := os.Stat(c.path + suffix); e == nil {
			usage.TransactionBytes += info.Size()
		}
	}
	return usage, err
}

func (c *Catalog) initEvidence() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS manifest_snapshots(
digest TEXT PRIMARY KEY,backup_id TEXT NOT NULL,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cleanup_evidence(
digest TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cleanup_attempts(
job TEXT NOT NULL,instance TEXT NOT NULL,attempt INTEGER NOT NULL,
status TEXT NOT NULL,at TEXT NOT NULL,detail TEXT NOT NULL,reason TEXT NOT NULL,
PRIMARY KEY(job,instance,attempt));`)
	return err
}

func storeManifest(ctx context.Context, tx *sql.Tx, id string, payload []byte) (string, error) {
	if len(payload) > 128<<20 || !json.Valid(payload) {
		return "", errors.New("固定清单无效或超过容量")
	}
	digest := ManifestDigest(payload)
	var conflicting string
	err := tx.QueryRowContext(ctx, "SELECT digest FROM manifest_snapshots WHERE backup_id=? AND digest<>? LIMIT 1", id, digest).Scan(&conflicting)
	if err == nil {
		return "", errors.New("同一备份编号对应不同清单，拒绝替换记录")
	}
	if !isNoRows(err) {
		return "", err
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT payload FROM manifest_snapshots WHERE digest=?", digest).Scan(&previous)
	if err == nil {
		if previous != string(payload) {
			return "", errors.New("固定清单编号或摘要冲突")
		}
		return digest, nil
	}
	if !isNoRows(err) {
		return "", err
	}
	if err := checkCapacity(ctx, tx, int64(len(payload))+4096); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO manifest_snapshots VALUES(?,?,?)", digest, id, string(payload))
	return digest, err
}

func (c *Catalog) snapshot(digest string) (string, error) {
	var payload string
	if err := c.db.QueryRow("SELECT payload FROM manifest_snapshots WHERE digest=?", digest).Scan(&payload); err != nil {
		return "", err
	}
	if ManifestDigest([]byte(payload)) != digest {
		return "", errors.New("固定清单摘要不符")
	}
	return payload, nil
}

// Legacy records are converted one transaction at a time. A failed conversion
// rolls back that record, and reopening continues with its original evidence.
func (c *Catalog) migrateEvidence() error {
	for {
		var id, payload string
		err := c.db.QueryRow("SELECT id,payload FROM backup_records WHERE length(coalesce(json_extract(payload,'$.manifest'),''))>0 LIMIT 1").Scan(&id, &payload)
		if isNoRows(err) {
			break
		}
		if err != nil {
			return err
		}
		var stored storedBackup
		if err = json.Unmarshal([]byte(payload), &stored); err != nil {
			return err
		}
		if ManifestDigest([]byte(stored.Manifest)) != stored.Record.Digest {
			return errors.New("迁移前备份清单摘要不符")
		}
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		digest, err := storeManifestMigration(tx, id, []byte(stored.Manifest))
		if err == nil {
			_, err = tx.Exec("UPDATE backup_records SET payload=json_set(json_remove(payload,'$.manifest'),'$.manifest_ref',?) WHERE id=?", digest, id)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	for {
		var id, payload string
		err := c.db.QueryRow("SELECT id,payload FROM backup_imports WHERE json_type(payload,'$.manifest')='object' LIMIT 1").Scan(&id, &payload)
		if isNoRows(err) {
			break
		}
		if err != nil {
			return err
		}
		if err = c.saveImport(id, []byte(payload), true); err != nil {
			return err
		}
	}
	for {
		var id, payload string
		err := c.db.QueryRow("SELECT id,manifest FROM restored_backups WHERE substr(manifest,1,9)<>'snapshot:' LIMIT 1").Scan(&id, &payload)
		if isNoRows(err) {
			break
		}
		if err != nil {
			return err
		}
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		ref, err := storeManifestMigration(tx, id, []byte(payload))
		if err == nil {
			_, err = tx.Exec("UPDATE restored_backups SET manifest=? WHERE id=? AND manifest=?", "snapshot:"+ref, id, payload)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return c.migrateCleanupEvidence()
}

// Migration moves already-counted evidence, so it does not reserve a second
// logical copy against the application's capacity limit.
func storeManifestMigration(tx *sql.Tx, id string, payload []byte) (string, error) {
	if len(payload) > 128<<20 || !json.Valid(payload) {
		return "", errors.New("迁移清单无效")
	}
	digest := ManifestDigest(payload)
	_, err := tx.Exec("INSERT INTO manifest_snapshots VALUES(?,?,?) ON CONFLICT(digest) DO NOTHING", digest, id, string(payload))
	if err != nil {
		return "", err
	}
	var existing string
	err = tx.QueryRow("SELECT payload FROM manifest_snapshots WHERE digest=?", digest).Scan(&existing)
	if err != nil {
		return "", err
	}
	if existing != string(payload) {
		return "", fmt.Errorf("固定清单冲突：%s", id)
	}
	return digest, nil
}
