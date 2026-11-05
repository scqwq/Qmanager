package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
)

func (s *Media) selectedMedia(ctx context.Context, source media.Source, request CleanupRequest) ([]media.Item, error) {
	if err := media.ValidateFilter(request.Filter); err != nil {
		return nil, err
	}
	var items []media.Item
	var err error
	if request.AllMatching {
		request.Filter.HideVideoCovers = true
		if len(request.IDs) != 0 {
			return nil, errors.New("选择方式冲突")
		}
		items, err = s.catalog.Selected(ctx, source.ID, request.Filter)
		if err != nil {
			return nil, err
		}
		for i := range items {
			items[i] = media.Bind(items[i], source)
		}
		if len(request.ExcludeIDs) > media.MaxFiles {
			return nil, errors.New("排除项超过上限")
		}
		excluded := map[string]bool{}
		for _, id := range request.ExcludeIDs {
			excluded[id] = true
		}
		kept := items[:0]
		for _, item := range items {
			if !excluded[item.InstanceID] {
				kept = append(kept, item)
			}
		}
		items = kept
	} else {
		if len(request.ExcludeIDs) != 0 {
			return nil, errors.New("选择方式冲突")
		}
		if len(request.IDs) == 0 || len(request.IDs) > media.MaxFiles {
			return nil, errors.New("请选择媒体文件")
		}
		seen := map[string]bool{}
		for _, id := range request.IDs {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			item, e := s.catalog.Item(source.ID, id)
			if e != nil {
				return nil, errors.New("部分所选记录已变化，请重新选择")
			}
			items = append(items, media.Bind(item, source))
		}
	}
	return items, nil
}

func (s *Media) currentMediaSource() (media.Source, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.state.Source == nil || s.state.Mode != "source" {
		return media.Source{}, errors.New("请先选择 QQ 媒体来源")
	}
	return *s.state.Source, nil
}

func (s *Media) SelectedBackupEstimate(ctx context.Context, request CleanupRequest) (mediabackup.Preview, error) {
	source, err := s.currentMediaSource()
	if err != nil {
		return mediabackup.Preview{}, err
	}
	items, err := s.selectedMedia(ctx, source, request)
	if err != nil {
		return mediabackup.Preview{}, err
	}
	return mediabackup.Plan(items, media.Filter{}), nil
}
