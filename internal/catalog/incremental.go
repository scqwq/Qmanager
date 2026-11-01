package catalog

import (
	"context"
	"encoding/json"
	"qqarchive/internal/media"
	"strings"
	"time"
)

func (c *Catalog) KnownPaths(ctx context.Context, source string) (map[string]bool, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT path FROM media_cache WHERE source=? LIMIT 200001", source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			return nil, err
		}
		out[path] = true
	}
	return out, rows.Err()
}

// Partial passes read only the current rows they actually checked. In
// particular, an empty name-only pass never decodes every cached JSON payload.
func (c *Catalog) activeForReport(ctx context.Context, source string, r *media.Report) ([]Record, error) {
	if !r.Partial {
		return c.ActiveContext(ctx, source)
	}
	paths := []string{}
	seen := map[string]bool{}
	itemPaths := map[string]bool{}
	for _, item := range r.Items {
		itemPaths[item.RelativePath] = true
		if !seen[item.RelativePath] {
			paths = append(paths, item.RelativePath)
			seen[item.RelativePath] = true
		}
	}
	for _, path := range r.Covered {
		if path == "." {
			return c.ActiveContext(ctx, source)
		}
		if !seen[path] {
			paths = append(paths, path)
			seen[path] = true
		}
	}
	out := []Record{}
	ids := map[string]bool{}
	read := func(query string, args ...any) error {
		rows, err := c.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row Record
			var payload, at string
			if err = rows.Scan(&payload, &row.Present, &row.Samples, &at); err != nil {
				return err
			}
			if err = json.Unmarshal([]byte(payload), &row.Item); err != nil {
				return err
			}
			row.Seen, err = time.Parse(time.RFC3339Nano, at)
			if err != nil {
				return err
			}
			row.Item.LastSeenAt = row.Seen
			if !ids[row.Item.InstanceID] {
				ids[row.Item.InstanceID] = true
				out = append(out, row)
			}
		}
		return rows.Err()
	}
	const base = "SELECT payload,present,samples,seen FROM instances WHERE source=? AND present=1 AND "
	for i := 0; i < len(paths); i += 256 {
		end := min(i+256, len(paths))
		args := []any{source}
		marks := []string{}
		for _, path := range paths[i:end] {
			marks = append(marks, "?")
			args = append(args, path)
		}
		if err := read(base+"path IN ("+strings.Join(marks, ",")+")", args...); err != nil {
			return nil, err
		}
	}
	// A proven-missing directory has no exact current file row. Prefix ranges
	// use the existing (source,present,path) index, with no LIKE wildcard parsing.
	for _, prefix := range r.Covered {
		if itemPaths[prefix] {
			continue
		}
		if err := read(base+"path>=? AND path<?", source, prefix+"/", prefix+"0"); err != nil {
			return nil, err
		}
	}
	return out, nil
}
