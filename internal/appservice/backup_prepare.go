package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"time"
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
	todo := []int{}
	for i := range items {
		items[i] = media.Bind(items[i], source)
		item := items[i]
		if item.Status == "recognized" && item.Bytes > 0 && item.Role != "temporary" {
			todo = append(todo, i)
		}
	}
	exclude := func(index int, reason string) {
		item := items[index]
		options.Excluded[item.InstanceID] = reason
		s.mu.Lock()
		if len(s.state.ExcludedItems) < media.MaxIssues {
			s.state.ExcludedItems = append(s.state.ExcludedItems, media.Issue{Path: item.RelativePath, Code: reason})
		}
		s.mu.Unlock()
	}
	for pass := 1; pass <= 3 && len(todo) > 0; pass++ {
		if pass > 1 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, options, ctx.Err()
			case <-timer.C:
			}
		}
		s.mu.Lock()
		s.state.CheckPass, s.state.CheckTotal, s.state.Scanned = pass, len(todo), 0
		s.mu.Unlock()
		r := media.Report{Partial: true, Complete: true, UpdateKind: "targeted", StartedAt: time.Now().UTC()}
		checked := []int{}
		for n, index := range todo {
			if err = ctx.Err(); err != nil {
				return nil, options, err
			}
			item := items[index]
			input, e := media.OpenItem(item)
			valid := e == nil && item.Identity != ""
			if input != nil {
				valid = valid && media.SameItem(input, item)
				input.Close()
			}
			if valid {
				r.Items = append(r.Items, item)
				checked = append(checked, index)
			} else {
				exclude(index, "unreadable_or_changed")
			}
			s.mu.Lock()
			s.state.Scanned = n + 1
			s.mu.Unlock()
		}
		todo = nil
		if len(checked) == 0 {
			break
		}
		r.FinishedAt = time.Now().UTC()
		r.Inspected = len(checked)
		if err = s.catalog.Apply(ctx, source.ID, &r, false, nil, false, r.FinishedAt); err != nil {
			return nil, options, err
		}
		for n, index := range checked {
			if r.Items[n].InstanceID != items[index].InstanceID {
				exclude(index, "instance_changed")
				continue
			}
			items[index] = media.Bind(r.Items[n], source)
			if items[index].Stability != "stable_heuristic" {
				todo = append(todo, index)
			}
		}
	}
	for _, index := range todo {
		exclude(index, "still_unstable")
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
