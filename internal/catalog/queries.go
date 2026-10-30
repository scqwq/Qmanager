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
	where, args, err := cacheWhere(source, f)
	if err != nil {
		return 0, 0, 0, err
	}
	condition := "c.status='recognized' AND c.bytes>0 AND c.role<>'temporary' AND json_extract(c.payload,'$.stability')='stable_heuristic'"
	var files, total int
	var bytes int64
	err = c.db.QueryRow("SELECT coalesce(sum(CASE WHEN "+condition+" THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN "+condition+" THEN c.bytes ELSE 0 END),0),count(*) FROM media_cache c WHERE "+where, args...).Scan(&files, &bytes, &total)
	return files, bytes, total - files, err
}
