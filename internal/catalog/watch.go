package catalog

import (
	"context"
	"errors"
	"time"
)

func (c *Catalog) RawEvent(source, path string, action uint32, at time.Time) error {
	stamp := at.UTC().Format(time.RFC3339Nano)
	var duplicate int
	err := c.db.QueryRow("SELECT count(*) FROM (SELECT source,path,action,at FROM watch_events ORDER BY id DESC LIMIT 1) WHERE source=? AND path=? AND action=? AND at=?", source, path, action, stamp).Scan(&duplicate)
	if err != nil {
		return err
	}
	if duplicate > 0 {
		return nil
	}
	if err = checkCapacity(context.Background(), c.db, int64(len(path)+len(source)+len(stamp))+256); err != nil {
		return err
	}
	result, err := c.db.Exec("INSERT INTO watch_events(source,path,action,at) SELECT ?,?,?,? WHERE (SELECT count(*) FROM watch_events)<100000", source, path, action, stamp)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		removed, e := c.db.Exec("DELETE FROM watch_events WHERE id IN (SELECT id FROM watch_events WHERE processed=1 ORDER BY id LIMIT 256)")
		if e != nil {
			return e
		}
		reclaimed, e := removed.RowsAffected()
		if e != nil {
			return e
		}
		if reclaimed == 0 {
			return errors.New("原生事件历史达到容量上限")
		}
		_, err = c.db.Exec("INSERT INTO watch_events(source,path,action,at) VALUES(?,?,?,?)", source, path, action, stamp)
	}
	return err
}
func (c *Catalog) LastScan(source string) time.Time {
	var value string
	if c.db.QueryRow("SELECT scanned FROM source_state WHERE source=?", source).Scan(&value) != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
