package appservice

import (
	"context"
	"qqarchive/internal/media"
	"time"
)

// observeSelection checks only the frozen instances. It never substitutes a new
// version, refreshes a stale identity, or borrows evidence from unrelated files.
// Both backup and cleanup use actual spaced observations, not time spent waiting.
func (s *Media) observeSelection(ctx context.Context, source media.Source, items []media.Item, blocked map[string]string) (map[string]string, error) {
	excluded := make(map[string]string, len(blocked))
	for id, reason := range blocked {
		excluded[id] = reason
	}
	todo := []int{}
	for i, item := range items {
		if excluded[item.InstanceID] == "" && item.Status == "recognized" && item.Bytes > 0 && item.Role != "temporary" {
			todo = append(todo, i)
		}
	}
	defer func() {
		s.mu.Lock()
		s.state.CheckWaiting = false
		s.mu.Unlock()
	}()
	for pass := 1; pass <= 3 && len(todo) > 0; pass++ {
		s.mu.Lock()
		s.state.CheckStage = "stability"
		s.state.CheckPass, s.state.CheckTotal, s.state.Scanned = pass, len(todo), 0
		s.state.CheckWaiting = pass > 1
		s.state.Bytes = 0
		s.mu.Unlock()
		if pass > 1 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return excluded, ctx.Err()
			case <-timer.C:
			}
		}
		s.mu.Lock()
		s.state.CheckWaiting = false
		s.mu.Unlock()
		r := media.Report{Partial: true, Complete: true, UpdateKind: "targeted", StartedAt: time.Now().UTC()}
		checked := []int{}
		for n, index := range todo {
			if err := ctx.Err(); err != nil {
				return excluded, err
			}
			item := items[index]
			input, err := media.OpenItem(item)
			valid := err == nil && item.Identity != ""
			if input != nil {
				valid = valid && media.SameItem(input, item)
				input.Close()
			}
			if valid {
				r.Items = append(r.Items, item)
				checked = append(checked, index)
			} else {
				excluded[item.InstanceID] = "unreadable_or_changed"
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
		if err := s.catalog.Apply(ctx, source.ID, &r, false, nil, false, r.FinishedAt); err != nil {
			return excluded, err
		}
		for n, index := range checked {
			if r.Items[n].InstanceID != items[index].InstanceID {
				excluded[items[index].InstanceID] = "instance_changed"
				continue
			}
			items[index] = media.Bind(r.Items[n], source)
			if items[index].Stability != "stable_heuristic" {
				todo = append(todo, index)
			}
		}
	}
	for _, index := range todo {
		excluded[items[index].InstanceID] = "still_unstable"
	}
	return excluded, nil
}

func selectionSkipMessage(code string) string {
	switch code {
	case "unreadable_or_changed":
		return "文件缺失、不可读或与所选记录不符，已跳过；请更新后重新选择"
	case "instance_changed":
		return "所选文件实例已变化，已跳过；请重新选择"
	case "still_unstable":
		return "多轮核对后文件仍未稳定，已跳过"
	default:
		return code
	}
}
