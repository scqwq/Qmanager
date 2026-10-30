package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"qqarchive/internal/media"
	"strings"
	"time"
)

// Project only the current instances for indexed UI queries. The event/version
// ledger remains authoritative; this projection may be regenerated from it.
func (c *Catalog) initCache() error {
	_, err := c.db.Exec(`CREATE TABLE IF NOT EXISTS media_cache(source TEXT NOT NULL,id TEXT PRIMARY KEY,path TEXT NOT NULL,kind TEXT NOT NULL,month TEXT NOT NULL,role TEXT NOT NULL,status TEXT NOT NULL,bytes INTEGER NOT NULL,first_seen TEXT NOT NULL,method TEXT NOT NULL,payload TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS media_cache_order ON media_cache(source,month DESC,path);
CREATE INDEX IF NOT EXISTS media_cache_type ON media_cache(source,kind,month DESC,path);
CREATE TABLE IF NOT EXISTS cache_meta(version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS source_reports(source TEXT PRIMARY KEY,payload TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS app_preferences(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS event_instance ON events(instance,id);`)
	if err != nil {
		return err
	}
	var count int
	if err = c.db.QueryRow("SELECT count(*) FROM cache_meta").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT OR REPLACE INTO media_cache SELECT source,id,path,coalesce(json_extract(payload,'$.kind'),''),coalesce(json_extract(payload,'$.source_month'),''),coalesce(json_extract(payload,'$.role'),''),coalesce(json_extract(payload,'$.status'),''),coalesce(json_extract(payload,'$.bytes'),0),coalesce(json_extract(payload,'$.first_seen_at'),''),coalesce(json_extract(payload,'$.observation_method'),''),payload FROM instances WHERE present=1;
INSERT INTO cache_meta VALUES(1);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (c *Catalog) loadRecords(id string, activeOnly bool) ([]Record, error) {
	query := "SELECT payload,present,samples,seen FROM instances WHERE source=?"
	if activeOnly {
		query += " AND present=1"
	}
	query += " ORDER BY rowid"
	rows, err := c.db.Query(query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var payload, seen string
		var present, samples int
		if err := rows.Scan(&payload, &present, &samples, &seen); err != nil {
			return nil, err
		}
		var item media.Item
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, seen)
		if err != nil {
			return nil, err
		}
		item.LastSeenAt = at
		out = append(out, Record{Item: item, Samples: samples, Present: present == 1, Seen: at})
		if len(out) > 500000 {
			return nil, errors.New("来源索引达到实例上限")
		}
	}
	return out, rows.Err()
}
func (c *Catalog) Active(id string) ([]Record, error) { return c.loadRecords(id, true) }
func (c *Catalog) ActiveItems(id string) (map[string]media.Item, error) {
	records, err := c.Active(id)
	if err != nil {
		return nil, err
	}
	items := make(map[string]media.Item, len(records))
	for _, r := range records {
		items[r.Item.RelativePath] = r.Item
	}
	return items, nil
}

func cacheWhere(source string, f media.Filter) (string, []interface{}, error) {
	clauses := []string{"c.source=?"}
	args := []interface{}{source}
	add := func(clause string, value interface{}) { clauses = append(clauses, clause); args = append(args, value) }
	if f.Kind != "" {
		add("c.kind=?", f.Kind)
	}
	if f.Role != "" {
		add("c.role=?", f.Role)
	}
	if f.Status != "" {
		add("c.status=?", f.Status)
	}
	if f.MinBytes > 0 {
		add("c.bytes>=?", f.MinBytes)
	}
	if f.MaxBytes > 0 {
		add("c.bytes<=?", f.MaxBytes)
	}
	// Date boundaries are whole UTC seconds; ignoring fractional seconds here
	// preserves all instants in local [start,nextDayStart), including DST days.
	at := func(op string, t time.Time) {
		add("substr(c.first_seen,1,19)"+op+"?", t.UTC().Format("2006-01-02T15:04:05"))
	}
	if f.View == "observed" {
		clauses = append(clauses, "c.first_seen<>''", "substr(c.first_seen,1,19)<>'0001-01-01T00:00:00'")
		if !f.IncludeBaseline {
			clauses = append(clauses, "c.method<>'baseline'")
		}
		if f.Month == "__unknown__" {
			clauses = append(clauses, "1=0")
		} else if f.Month != "" {
			start, e := time.ParseInLocation("2006-01", f.Month, time.Local)
			if e != nil {
				return "", nil, e
			}
			at(">=", start)
			at("<", start.AddDate(0, 1, 0))
		}
	} else if f.Month != "" {
		month := f.Month
		if month == "__unknown__" {
			month = ""
		}
		add("c.month=?", month)
	}
	if f.From != "" {
		start, e := time.ParseInLocation("2006-01-02", f.From, time.Local)
		if e != nil {
			return "", nil, e
		}
		at(">=", start)
	}
	if f.To != "" {
		end, e := time.ParseInLocation("2006-01-02", f.To, time.Local)
		if e != nil {
			return "", nil, e
		}
		at("<", end.AddDate(0, 0, 1))
	}
	return strings.Join(clauses, " AND "), args, nil
}
func (c *Catalog) Page(ctx context.Context, source string, f media.Filter) (media.Page, error) {
	p := media.Page{Items: []media.Item{}}
	where, args, err := cacheWhere(source, f)
	if err != nil {
		return p, err
	}
	if err = c.db.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(c.bytes),0) FROM media_cache c WHERE "+where, args...).Scan(&p.Total, &p.Bytes); err != nil {
		return p, err
	}
	if f.View == "observed" {
		all := f
		all.IncludeBaseline = true
		baselineWhere, baselineArgs, e := cacheWhere(source, all)
		if e != nil {
			return p, e
		}
		if e = c.db.QueryRowContext(ctx, "SELECT count(*) FROM media_cache c WHERE "+baselineWhere+" AND c.method='baseline'", baselineArgs...).Scan(&p.BaselineFiles); e != nil {
			return p, e
		}
	}
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 60
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := c.db.QueryContext(ctx, "SELECT c.payload,i.seen FROM media_cache c JOIN instances i ON i.id=c.id WHERE "+where+" ORDER BY c.month DESC,c.path,c.id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload, seen string
		if err = rows.Scan(&payload, &seen); err != nil {
			return p, err
		}
		var item media.Item
		if err = json.Unmarshal([]byte(payload), &item); err != nil {
			return p, err
		}
		if at, e := time.Parse(time.RFC3339Nano, seen); e == nil {
			item.LastSeenAt = at
		}
		p.Items = append(p.Items, item)
	}
	return p, rows.Err()
}
func (c *Catalog) Buckets(source, view string, includeBaseline ...bool) ([]media.Bucket, error) {
	out := []media.Bucket{}
	if view != "observed" {
		rows, err := c.db.Query("SELECT kind,month,role,count(*),coalesce(sum(bytes),0) FROM media_cache WHERE source=? GROUP BY kind,month,role ORDER BY month DESC,kind,role", source)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var b media.Bucket
			if err = rows.Scan(&b.Kind, &b.Month, &b.Role, &b.Files, &b.Bytes); err != nil {
				return nil, err
			}
			out = append(out, b)
		}
		return out, rows.Err()
	}
	condition := " AND method<>'baseline'"
	if len(includeBaseline) > 0 && includeBaseline[0] {
		condition = ""
	}
	rows, err := c.db.Query("SELECT kind,first_seen,role,count(*),sum(bytes) FROM media_cache WHERE source=?"+condition+" AND first_seen<>'' GROUP BY kind,first_seen,role", source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]*media.Bucket{}
	for rows.Next() {
		var b media.Bucket
		var at string
		if err = rows.Scan(&b.Kind, &at, &b.Role, &b.Files, &b.Bytes); err != nil {
			return nil, err
		}
		t, e := time.Parse(time.RFC3339Nano, at)
		if e != nil || t.IsZero() {
			continue
		}
		b.Month = t.Local().Format("2006-01")
		key := b.Kind + "/" + b.Month + "/" + b.Role
		if old := groups[key]; old != nil {
			old.Files += b.Files
			old.Bytes += b.Bytes
		} else {
			copy := b
			groups[key] = &copy
		}
	}
	for _, b := range groups {
		out = append(out, *b)
	}
	return out, rows.Err()
}
func (c *Catalog) Report(source string) (*media.Report, error) {
	var payload string
	err := c.db.QueryRow("SELECT payload FROM source_reports WHERE source=?", source).Scan(&payload)
	r := &media.Report{Issues: []media.Issue{}, Buckets: []media.Bucket{}}
	if err == nil {
		if err = json.Unmarshal([]byte(payload), r); err != nil {
			return nil, err
		}
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	buckets, err := c.Buckets(source, "")
	if err != nil {
		return nil, err
	}
	r.Buckets = buckets
	r.Files = 0
	r.Bytes = 0
	for _, b := range buckets {
		r.Files += b.Files
		r.Bytes += b.Bytes
	}
	if r.Files == 0 && payload == "" && c.LastScan(source).IsZero() {
		return nil, nil
	}
	if r.FinishedAt.IsZero() {
		r.FinishedAt = c.LastScan(source)
	}
	return r, nil
}
func project(tx *sql.Tx, source string, item media.Item, payload []byte) error {
	_, err := tx.Exec(`INSERT INTO media_cache VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET path=excluded.path,kind=excluded.kind,month=excluded.month,role=excluded.role,status=excluded.status,bytes=excluded.bytes,first_seen=excluded.first_seen,method=excluded.method,payload=excluded.payload`, source, item.InstanceID, item.RelativePath, item.Kind, item.SourceMonth, item.Role, item.Status, item.Bytes, item.FirstSeenAt.UTC().Format(time.RFC3339Nano), item.Method, string(payload))
	return err
}
func touch(tx *sql.Tx, ids []string, at string) error {
	for offset := 0; offset < len(ids); offset += 400 {
		end := min(len(ids), offset+400)
		args := []interface{}{at}
		for _, id := range ids[offset:end] {
			args = append(args, id)
		}
		q := "UPDATE instances SET seen=? WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", end-offset), ",") + ")"
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("更新观察时间: %w", err)
		}
	}
	return nil
}
