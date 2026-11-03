package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
)

// Freeze selection from the index, then observe only those exact instances.
// Successful observations retain their original first-seen/history evidence.
// A missing/replaced/unreadable candidate is excluded, never substituted.
func (s *Media) prepareBackup(ctx context.Context, source media.Source, f media.Filter) ([]media.Item, mediabackup.BuildOptions, error) {
	options := mediabackup.BuildOptions{Excluded: map[string]string{}}
	items, err := s.catalog.Selected(ctx, source.ID, f)
	if err != nil {
		return nil, options, err
	}
	for i := range items {
		items[i] = media.Bind(items[i], source)
	}
	options.Excluded, err = s.observeSelection(ctx, source, items, nil)
	for _, item := range items {
		if reason := options.Excluded[item.InstanceID]; reason != "" {
			s.mu.Lock()
			if len(s.state.ExcludedItems) < media.MaxIssues {
				s.state.ExcludedItems = append(s.state.ExcludedItems, media.Issue{Path: item.RelativePath, Code: reason})
			}
			s.mu.Unlock()
		}
	}
	if err != nil {
		return nil, options, err
	}
	eligible := 0
	for _, item := range items {
		if item.Status == "recognized" && item.Bytes > 0 && item.Role != "temporary" && item.Stability == "stable_heuristic" && options.Excluded[item.InstanceID] == "" {
			eligible++
		}
	}
	if eligible == 0 {
		return nil, options, errors.New("本次范围核对后没有可备份文件：文件可能缺失、变化、不可读或尚未稳定，请查看未纳入说明")
	}
	return items, options, nil
}
