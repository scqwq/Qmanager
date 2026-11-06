package appservice

import (
	"context"
	"errors"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"sort"
	"time"
)

func (s *Media) Page(f media.Filter) media.Page {
	f.HideVideoCovers = true
	s.mu.RLock()
	if s.state.Mode == "backup" {
		p := media.Paginate(s.items, f)
		covers := media.MatchVideoCovers(p.Items, s.items)
		for i := range p.Items {
			p.Items[i].CoverID = covers[p.Items[i].ID].ID
		}
		s.mu.RUnlock()
		return p
	}
	source := s.state.Source
	s.mu.RUnlock()
	if source == nil {
		return media.Page{Items: []media.Item{}}
	}
	p, err := s.catalog.Page(context.Background(), source.ID, f)
	if err != nil {
		s.fail(err)
		return media.Page{Items: []media.Item{}}
	}
	covers, err := s.catalog.VideoCovers(context.Background(), source.ID, p.Items)
	if err != nil {
		s.fail(err)
	}
	for i := range p.Items {
		p.Items[i] = media.Bind(p.Items[i], *source)
		p.Items[i].CoverID = covers[p.Items[i].ID].ID
	}
	return p
}
func (s *Media) History(id string) ([]catalog.History, error) {
	item, ok := s.lookup(id)
	s.mu.RLock()
	if s.state.Mode == "backup" && ok {
		h := append([]catalog.History{}, s.archiveHistory[item.InstanceID]...)
		s.mu.RUnlock()
		return h, nil
	}
	s.mu.RUnlock()
	if !ok {
		return nil, errors.New("文件不在当前视图")
	}
	return s.catalog.History(item.InstanceID)
}

func report(items []media.Item) *media.Report {
	r := &media.Report{Complete: true, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Issues: []media.Issue{}, Buckets: []media.Bucket{}, Files: len(items)}
	b := map[string]*media.Bucket{}
	for _, item := range items {
		r.Bytes += item.Bytes
		k := item.Kind + item.SourceMonth + item.Role
		if b[k] == nil {
			b[k] = &media.Bucket{Kind: item.Kind, Month: item.SourceMonth, Role: item.Role}
		}
		b[k].Files++
		b[k].Bytes += item.Bytes
	}
	keys := []string{}
	for k := range b {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.Buckets = append(r.Buckets, *b[k])
	}
	return r
}

func (s *Media) ExportIndex(ctx context.Context, target string) (string, error) {
	protected, err := s.protected()
	if err != nil {
		return "", err
	}
	for _, root := range protected {
		if media.Overlap(root, target) {
			return "", errors.New("索引备份目标与来源或应用数据重叠")
		}
	}
	return s.catalog.Export(ctx, target)
}

func (s *Media) DataPath() string { return filepath.Join(s.private, "catalog.sqlite") }
func (s *Media) Buckets(view string, includeBaseline ...bool) []media.Bucket {
	s.mu.RLock()
	if s.state.Mode != "backup" {
		source := s.state.Source
		s.mu.RUnlock()
		if source == nil {
			return []media.Bucket{}
		}
		b, err := s.catalog.Buckets(source.ID, view, includeBaseline...)
		if err != nil {
			s.fail(err)
			return []media.Bucket{}
		}
		return b
	}
	defer s.mu.RUnlock()
	items := make([]media.Item, 0, len(s.items))
	for _, item := range s.items {
		if view == "observed" {
			if (item.Method == "baseline" && !(len(includeBaseline) > 0 && includeBaseline[0])) || item.FirstSeenAt.IsZero() {
				continue
			}
			item.SourceMonth = item.FirstSeenAt.Local().Format("2006-01")
		}
		items = append(items, item)
	}
	return report(items).Buckets
}
