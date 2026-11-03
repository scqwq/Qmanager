package catalog

import (
	"context"
	"encoding/json"
	"qqarchive/internal/media"
	"strings"
)

// VideoCovers uses the existing source/month/path projection, independently of
// page/date/size filters. No persistent association or schema migration is needed.
func (c *Catalog) VideoCovers(ctx context.Context, source string, videos []media.Item) (map[string]media.Item, error) {
	clauses := []string{}
	args := []interface{}{source}
	seen := map[string]bool{}
	for _, video := range videos {
		p := media.VideoCoverPath(video)
		key := strings.ToLower(p)
		if p == "" || seen[key] {
			continue
		}
		seen[key] = true
		clauses = append(clauses, "(c.month=? AND c.path=? COLLATE NOCASE)")
		args = append(args, video.SourceMonth, p)
	}
	if len(clauses) == 0 {
		return map[string]media.Item{}, nil
	}
	rows, err := c.db.QueryContext(ctx, "SELECT c.payload FROM media_cache c WHERE c.source=? AND c.kind='video' AND c.role='cover' AND ("+strings.Join(clauses, " OR ")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []media.Item{}
	for rows.Next() {
		var payload string
		var item media.Item
		if err = rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, err
		}
		candidates = append(candidates, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return media.MatchVideoCovers(videos, candidates), nil
}
