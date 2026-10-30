package catalog

import (
	"errors"
	"time"
)

func (c *Catalog) RawEvent(source, path string, action uint32, at time.Time) error {
	result, err := c.db.Exec("INSERT INTO watch_events(source,path,action,at) SELECT ?,?,?,? WHERE (SELECT count(*) FROM watch_events)<100000", source, path, action, at.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return errors.New("原生事件历史达到容量上限")
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
