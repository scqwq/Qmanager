package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"qqarchive/internal/media"
	"time"
)

type CleanupJob struct {
	Archived    bool         `json:"archived,omitempty"`
	Executed    bool         `json:"executed,omitempty"`
	Unavailable int          `json:"unavailable"`
	Missing     int          `json:"missing"`
	BackupIDs   []string     `json:"backup_ids,omitempty"`
	ID          string       `json:"id"`
	SourceID    string       `json:"source_id"`
	SourceName  string       `json:"source_name"`
	Root        string       `json:"root"`
	State       string       `json:"state"`
	CreatedAt   time.Time    `json:"created_at"`
	ExpiresAt   time.Time    `json:"expires_at"`
	Filter      media.Filter `json:"filter"`
	Total       int          `json:"total"`
	Ready       int          `json:"ready"`
	Pending     int          `json:"pending"`
	Skipped     int          `json:"skipped"`
	Recycled    int          `json:"recycled"`
	Failed      int          `json:"failed"`
	Uncertain   int          `json:"uncertain"`
	Bytes       int64        `json:"bytes"`
}
type CleanupItem struct {
	Reason    string     `json:"reason,omitempty"`
	UpdatedAt time.Time  `json:"updated_at,omitempty"`
	Attempts  int        `json:"attempts,omitempty"`
	Item      media.Item `json:"item"`
	SHA256    string     `json:"sha256"`
	Status    string     `json:"status"`
	Detail    string     `json:"detail"`
}

func (c *Catalog) initCleanup() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS cleanup_jobs(id TEXT PRIMARY KEY,source TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cleanup_items(job TEXT NOT NULL,instance TEXT NOT NULL,status TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(job,instance));
CREATE INDEX IF NOT EXISTS cleanup_status ON cleanup_items(job,status);`)
	return err
}
func (c *Catalog) RecoverCleanup() error {
	_, err := c.db.Exec("UPDATE cleanup_items SET status='uncertain',payload=json_set(payload,'$.status','uncertain','$.detail','应用中断，回收结果待核对','$.reason','outcome_unknown') WHERE status='processing'; UPDATE cleanup_jobs SET state='interrupted' WHERE state IN ('preparing','running');")
	if err == nil {
		_, err = c.db.Exec("UPDATE cleanup_attempts SET status='uncertain',detail='应用中断，回收结果待核对' WHERE status='processing'")
	}
	return err
}
func (c *Catalog) LatestCleanup(source string) (*CleanupJob, error) {
	var id string
	err := c.db.QueryRow("SELECT id FROM cleanup_jobs WHERE source=? ORDER BY rowid DESC LIMIT 1", source).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	job, err := c.Cleanup(id)
	return &job, err
}
func (c *Catalog) PrepareCleanup(id string) error {
	job, err := c.Cleanup(id)
	if err != nil {
		return err
	}
	job.ExpiresAt = time.Now().UTC().Add(30 * time.Minute)
	job.State = "prepared"
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = c.db.Exec("UPDATE cleanup_jobs SET state='prepared',payload=? WHERE id=?", string(b), id)
	return err
}
func (c *Catalog) CreateCleanup(ctx context.Context, source media.Source, f media.Filter, items []media.Item, backupIDs ...string) (CleanupJob, error) {
	if _, err := c.Sweep(ctx, time.Now()); err != nil {
		return CleanupJob{}, err
	}
	job := CleanupJob{ID: rand.Text(), SourceID: source.ID, SourceName: source.Name, Root: source.Resolved, State: "preparing", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(30 * time.Minute), Filter: f, Total: len(items)}
	job.BackupIDs = append([]string(nil), backupIDs...)
	job.Pending = len(items)
	if len(items) == 0 || len(items) > media.MaxFiles {
		return job, errors.New("请选择 1～200000 个媒体文件")
	}
	var events int
	if err := c.db.QueryRow("SELECT count(*) FROM events").Scan(&events); err != nil {
		return job, err
	}
	if events+len(items) > 1000000 {
		return job, errors.New("观察历史没有足够空间记录本次删除")
	}
	var jobs int
	if err := c.db.QueryRow("SELECT count(*) FROM cleanup_jobs WHERE state<>'archived'").Scan(&jobs); err != nil {
		return job, err
	}
	if jobs >= 1000 {
		return job, errors.New("媒体删除记录达到上限，请先备份索引并维护")
	}
	refs, err := c.cleanupReferences(job)
	if err != nil {
		return job, err
	}
	var budget int64
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return job, err
		}
		b, err := json.Marshal(CleanupItem{Item: item, Status: "pending"})
		if err != nil {
			return job, err
		}
		budget += 1024
		if len(refs[item.InstanceID]) == 0 {
			budget += int64(len(b))
		}
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
	if err = checkCapacity(ctx, tx, budget); err != nil {
		return job, err
	}
	b, _ := json.Marshal(job)
	if _, err = tx.ExecContext(ctx, "INSERT INTO cleanup_jobs VALUES(?,?,?,?)", job.ID, job.SourceID, job.State, string(b)); err != nil {
		return job, err
	}
	statement, err := tx.PrepareContext(ctx, "INSERT INTO cleanup_items VALUES(?,?,?,?)")
	if err != nil {
		return job, err
	}
	defer statement.Close()
	for _, item := range items {
		b, e := encodeCleanup(tx, job, CleanupItem{Item: item, Status: "pending", UpdatedAt: job.CreatedAt}, refs)
		if e != nil {
			return job, e
		}
		if _, err = statement.ExecContext(ctx, job.ID, item.InstanceID, "pending", string(b)); err != nil {
			return job, err
		}
	}
	return job, tx.Commit()
}
func (c *Catalog) Cleanup(id string) (CleanupJob, error) {
	var job CleanupJob
	var payload, state string
	err := c.db.QueryRow("SELECT payload,state FROM cleanup_jobs WHERE id=?", id).Scan(&payload, &state)
	if err != nil {
		return job, err
	}
	if err = json.Unmarshal([]byte(payload), &job); err != nil {
		return job, err
	}
	job.State = state
	if job.Archived {
		return job, nil
	}
	job.Ready = 0
	job.Pending = 0
	job.Skipped = 0
	job.Recycled = 0
	job.Failed = 0
	job.Uncertain = 0
	job.Bytes = 0
	job.Unavailable, job.Missing = 0, 0
	rows, err := c.db.Query("SELECT status,count(*),coalesce(sum(coalesce(json_extract(payload,'$.bytes'),json_extract(payload,'$.item.bytes'))),0),sum(CASE WHEN json_extract(payload,'$.reason')='unavailable' THEN 1 ELSE 0 END),sum(CASE WHEN json_extract(payload,'$.reason')='missing' THEN 1 ELSE 0 END) FROM cleanup_items WHERE job=? GROUP BY status", id)
	if err != nil {
		return job, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		var size int64
		var unavailable, missing int
		if err = rows.Scan(&status, &n, &size, &unavailable, &missing); err != nil {
			return job, err
		}
		job.Unavailable += unavailable
		job.Missing += missing
		switch status {
		case "pending":
			job.Pending = n
		case "ready":
			job.Ready = n
			job.Bytes += size
		case "recycled":
			job.Recycled = n
		case "skipped":
			job.Skipped = n
		case "failed":
			job.Failed = n
		case "uncertain", "processing":
			job.Uncertain += n
		}
	}
	return job, rows.Err()
}
func (c *Catalog) CleanupItems(id string, offset, limit int) ([]CleanupItem, error) {
	if limit < 1 || limit > 200000 {
		limit = 60
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := c.db.Query("SELECT payload,status FROM cleanup_items WHERE job=? ORDER BY rowid LIMIT ? OFFSET ?", id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := []struct{ payload, status string }{}
	for rows.Next() {
		var b, status string
		if err = rows.Scan(&b, &status); err != nil {
			return nil, err
		}
		raw = append(raw, struct{ payload, status string }{b, status})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]CleanupItem, 0, len(raw))
	cache := map[string]map[int]manifestItem{}
	for _, entry := range raw {
		row, e := c.decodeCleanup(entry.payload, cache)
		if e != nil {
			return nil, e
		}
		row.Status = entry.status
		out = append(out, row)
	}
	return out, nil
}
func (c *Catalog) CleanupState(id, state string) error {
	if state == "running" {
		_, err := c.db.Exec("UPDATE cleanup_jobs SET state=?,payload=json_set(payload,'$.executed',json('true')) WHERE id=?", state, id)
		return err
	}
	_, err := c.db.Exec("UPDATE cleanup_jobs SET state=? WHERE id=?", state, id)
	return err
}
func (c *Catalog) SaveCleanupItem(ctx context.Context, job CleanupJob, row CleanupItem) error {
	refs, err := c.cleanupReferences(job)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous cleanupStored
	var oldPayload string
	if err = tx.QueryRowContext(ctx, "SELECT payload FROM cleanup_items WHERE job=? AND instance=?", job.ID, row.Item.InstanceID).Scan(&oldPayload); err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(oldPayload), &previous); err != nil {
		return err
	}
	row.Attempts = previous.Attempts
	if row.Status == "processing" {
		if err = checkCapacity(ctx, tx, 64<<10); err != nil {
			return err
		}
		row.Attempts++
	}
	row.UpdatedAt = time.Now().UTC()
	b, err := encodeCleanup(tx, job, row, refs)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE cleanup_items SET status=?,payload=? WHERE job=? AND instance=?", row.Status, string(b), job.ID, row.Item.InstanceID); err != nil {
		return err
	}
	if row.Attempts > 0 && (row.Status == "processing" || row.Status == "recycled" || row.Status == "failed" || row.Status == "uncertain") {
		if _, err = tx.ExecContext(ctx, "INSERT INTO cleanup_attempts VALUES(?,?,?,?,?,?,?) ON CONFLICT(job,instance,attempt) DO UPDATE SET status=excluded.status,at=excluded.at,detail=excluded.detail,reason=excluded.reason", job.ID, row.Item.InstanceID, row.Attempts, row.Status, row.UpdatedAt.Format(time.RFC3339Nano), row.Detail, row.Reason); err != nil {
			return err
		}
	}
	if row.Status == "skipped" && row.Reason == "missing" {
		changed, e := tx.ExecContext(ctx, "UPDATE instances SET present=0 WHERE id=? AND source=? AND present=1", row.Item.InstanceID, job.SourceID)
		if e != nil {
			return e
		}
		n, e := changed.RowsAffected()
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM media_cache WHERE id=? AND source=?", row.Item.InstanceID, job.SourceID); e != nil {
			return e
		}
		if n > 0 {
			if _, e = tx.ExecContext(ctx, "INSERT INTO events(source,instance,at,kind,detail) VALUES(?,?,?,?,?)", job.SourceID, row.Item.InstanceID, time.Now().UTC().Format(time.RFC3339Nano), "missing", "selected_path_checked_missing"); e != nil {
				return e
			}
		}
	}
	if row.Status == "recycled" {
		if _, err = tx.ExecContext(ctx, "UPDATE instances SET present=0 WHERE id=? AND source=?", row.Item.InstanceID, job.SourceID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM media_cache WHERE id=? AND source=?", row.Item.InstanceID, job.SourceID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO events(source,instance,at,kind,detail) VALUES(?,?,?,?,?)", job.SourceID, row.Item.InstanceID, time.Now().UTC().Format(time.RFC3339Nano), "user_recycled", job.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
