package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"qqarchive/internal/media"
	"time"
)

func (c *Catalog) Item(source, id string) (media.Item, error) {
	var payload, seen string
	err := c.db.QueryRow("SELECT c.payload,i.seen FROM media_cache c JOIN instances i ON i.id=c.id WHERE c.source=? AND c.id=?", source, id).Scan(&payload, &seen)
	if err != nil {
		return media.Item{}, err
	}
	var item media.Item
	err = json.Unmarshal([]byte(payload), &item)
	if at, e := time.Parse(time.RFC3339Nano, seen); e == nil {
		item.LastSeenAt = at
	}
	return item, err
}
func (c *Catalog) Preference(key string) (string, error) {
	var value string
	err := c.db.QueryRow("SELECT value FROM app_preferences WHERE key=?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}
func (c *Catalog) SetPreference(key, value string) error {
	_, err := c.db.Exec("INSERT INTO app_preferences VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (c *Catalog) Selected(ctx context.Context, source string, f media.Filter) ([]media.Item, error) {
	where, args, err := cacheWhere(source, f)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.QueryContext(ctx, "SELECT c.payload,i.seen FROM media_cache c JOIN instances i ON i.id=c.id WHERE "+where+" ORDER BY c.month DESC,c.path LIMIT 200001", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []media.Item{}
	for rows.Next() {
		var payload, seen string
		if err = rows.Scan(&payload, &seen); err != nil {
			return nil, err
		}
		var item media.Item
		if err = json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		if at, e := time.Parse(time.RFC3339Nano, seen); e == nil {
			item.LastSeenAt = at
		}
		out = append(out, item)
		if len(out) > media.MaxFiles {
			return nil, errors.New("所选文件超过索引读取上限")
		}
	}
	return out, rows.Err()
}
func (c *Catalog) Eligible(source string, f media.Filter) (int, int64, int, error) {
	files, bytes, reasons, err := c.BackupStats(source, f)
	excluded := 0
	for _, count := range reasons {
		excluded += count
	}
	return files, bytes, excluded, err
}

// Group exclusions with the same precedence as mediabackup.reason, without
// loading every media payload into the UI process.
func (c *Catalog) BackupStats(source string, f media.Filter) (int, int64, map[string]int, error) {
	p, err := c.BackupPlan(source, f)
	if p.Pending > 0 {
		p.ExcludedReasons["pending"] = p.Pending
	}
	return p.Files, p.Bytes, p.ExcludedReasons, err
}

type BackupCounts struct {
	Files, Pending      int
	Bytes, PendingBytes int64
	ExcludedReasons     map[string]int
}

func (c *Catalog) BackupPlan(source string, f media.Filter) (BackupCounts, error) {
	p := BackupCounts{ExcludedReasons: map[string]int{}}
	where, args, err := cacheWhere(source, f)
	if err != nil {
		return p, err
	}
	reason := "CASE WHEN c.status<>'recognized' THEN c.status WHEN c.bytes=0 THEN 'empty' WHEN c.role='temporary' THEN 'temporary' WHEN coalesce(json_extract(c.payload,'$.stability'),'')<>'stable_heuristic' THEN 'pending' ELSE '' END"
	rows, err := c.db.Query("SELECT "+reason+" AS reason,count(*),coalesce(sum(c.bytes),0) FROM media_cache c WHERE "+where+" GROUP BY reason", args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var why string
		var count int
		var size int64
		if err = rows.Scan(&why, &count, &size); err != nil {
			return p, err
		}
		if why == "" {
			p.Files += count
			p.Bytes += size
		} else if why == "pending" {
			p.Pending += count
			p.PendingBytes += size
		} else {
			p.ExcludedReasons[why] = count
		}
	}
	return p, rows.Err()
}
