package catalog

import "context"

// Read path-only backup exclusions; do not load manifests or update presence.
func (c *Catalog) BackupPaths(ctx context.Context) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, "SELECT json_extract(payload,'$.record.path') FROM backup_records LIMIT 10001")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := []string{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}
