package appservice

import (
	"context"
	"qqarchive/internal/media"
)

func (s *Media) videoCover(video media.Item) (media.Item, bool) {
	s.mu.RLock()
	if s.state.Mode == "backup" {
		if _, ok := s.byID[video.ID]; !ok {
			s.mu.RUnlock()
			return media.Item{}, false
		}
		cover, ok := media.MatchVideoCovers([]media.Item{video}, s.items)[video.ID]
		s.mu.RUnlock()
		return cover, ok
	}
	source := s.state.Source
	s.mu.RUnlock()
	if source == nil {
		return media.Item{}, false
	}
	if _, err := s.catalog.Item(source.ID, video.ID); err != nil {
		return media.Item{}, false
	}
	covers, err := s.catalog.VideoCovers(context.Background(), source.ID, []media.Item{video})
	cover, ok := covers[video.ID]
	return media.Bind(cover, *source), err == nil && ok
}

// Navigation counts reflect standalone cards; inventory and backup counts retain
// every physical file. An explicit role also narrows navigation to that role.
func (s *Media) FilterBuckets(f media.Filter) []media.Bucket {
	buckets := s.Buckets(f.View, f.IncludeBaseline)
	out := make([]media.Bucket, 0, len(buckets))
	for _, b := range buckets {
		if media.VisibleRole(b.Kind, b.Role, f.Role) && (f.Role == "" || f.Role == b.Role) {
			out = append(out, b)
		}
	}
	return out
}
