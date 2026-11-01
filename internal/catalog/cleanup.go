package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"qqarchive/internal/media"
	"time"
)

type CleanupJob struct {
	BackupIDs  []string     `json:"backup_ids,omitempty"`
	ID         string       `json:"id"`
	SourceID   string       `json:"source_id"`
	SourceName string       `json:"source_name"`
	Root       string       `json:"root"`
	State      string       `json:"state"`
	CreatedAt  time.Time    `json:"created_at"`
	ExpiresAt  time.Time    `json:"expires_at"`
	Filter     media.Filter `json:"filter"`
	Total      int          `json:"total"`
	Ready      int          `json:"ready"`
	Pending    int          `json:"pending"`
	Skipped    int          `json:"skipped"`
	Recycled   int          `json:"recycled"`
	Failed     int          `json:"failed"`
	Uncertain  int          `json:"uncertain"`
	Bytes      int64        `json:"bytes"`
}
type CleanupItem struct {
	Item   media.Item `json:"item"`
	SHA256 string     `json:"sha256"`
	Status string     `json:"status"`
	Detail string     `json:"detail"`
}

func (c *Catalog) initCleanup() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS cleanup_jobs(id TEXT PRIMARY KEY,source TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS cleanup_items(job TEXT NOT NULL,instance TEXT NOT NULL,status TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(job,instance));
CREATE INDEX IF NOT EXISTS cleanup_status ON cleanup_items(job,status);`)
	return err
}
func (c *Catalog) RecoverCleanup() error {
	_, err := c.db.Exec("UPDATE cleanup_items SET status='uncertain' WHERE status='processing'; UPDATE cleanup_jobs SET state='interrupted' WHERE state IN ('preparing','running');")
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
	if err := c.db.QueryRow("SELECT count(*) FROM cleanup_jobs").Scan(&jobs); err != nil {
		return job, err
	}
	if jobs >= 1000 {
		return job, errors.New("媒体删除记录达到上限，请先备份索引并维护")
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
		budget += int64(len(b))*2 + 4096
	}
	if info, err := os.Stat(c.path); err != nil || info.Size()+budget > 512*1024*1024 {
		return job, errors.New("索引没有足够容量记录本次删除，请缩小范围")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return job, err
	}
	defer tx.Rollback()
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
		b, e := json.Marshal(CleanupItem{Item: item, Status: "pending"})
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
	job.Ready = 0
	job.Pending = 0
	job.Skipped = 0
	job.Recycled = 0
	job.Failed = 0
	job.Uncertain = 0
	job.Bytes = 0
	rows, err := c.db.Query("SELECT status,count(*),coalesce(sum(json_extract(payload,'$.item.bytes')),0) FROM cleanup_items WHERE job=? GROUP BY status", id)
	if err != nil {
		return job, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		var size int64
		if err = rows.Scan(&status, &n, &size); err != nil {
			return job, err
		}
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
	out := []CleanupItem{}
	for rows.Next() {
		var b, status string
		var row CleanupItem
		if err = rows.Scan(&b, &status); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &row); err != nil {
			return nil, err
		}
		row.Status = status
		out = append(out, row)
	}
	return out, rows.Err()
}
func (c *Catalog) CleanupState(id, state string) error {
	_, err := c.db.Exec("UPDATE cleanup_jobs SET state=? WHERE id=?", state, id)
	return err
}
func (c *Catalog) SaveCleanupItem(ctx context.Context, job CleanupJob, row CleanupItem) error {
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE cleanup_items SET status=?,payload=? WHERE job=? AND instance=?", row.Status, string(b), job.ID, row.Item.InstanceID); err != nil {
		return err
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
