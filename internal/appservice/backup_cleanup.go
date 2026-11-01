package appservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"strings"
)

func sameBackupVersion(a, b media.Item) bool {
	return a.InstanceID == b.InstanceID && a.Identity != "" && a.Identity == b.Identity && a.RelativePath == b.RelativePath && a.Kind == b.Kind && a.Role == b.Role && a.Bytes == b.Bytes && a.ModifiedAt.Equal(b.ModifiedAt)
}
func validBackupHash(hash string) bool {
	return len(hash) == 64 && strings.Trim(hash, "0123456789abcdef") == ""
}

type backedCandidate struct {
	item     media.Item
	hash     string
	skip     string
	conflict bool
}

// The recorded, already-validated manifest remains after deleting the local
// package. No missing package is treated as a successful cloud verification.
func (s *Media) PrepareBackupSourceDelete(parent context.Context, ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return errors.New("请选择同一原来源的 1～100 份备份")
	}
	return s.backupJob(parent, "preparing_delete", func(ctx context.Context) error {
		s.mu.Lock()
		s.state.Cleanup = nil
		s.mu.Unlock()
		seen := map[string]bool{}
		unique := []string{}
		candidates := map[string]*backedCandidate{}
		order := []string{}
		sourceID := ""
		metadata := 0
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			unique = append(unique, id)
			row, err := s.catalog.Backup(id)
			if err != nil {
				return err
			}
			metadata += len(row.Manifest)
			if metadata > 64*1024*1024 {
				return errors.New("所选历史清单过大，请分批清理")
			}
			if catalog.ManifestDigest([]byte(row.Manifest)) != row.Digest {
				return errors.New("备份历史清单校验不符，未清理")
			}
			var manifest mediabackup.Manifest
			if err = json.Unmarshal([]byte(row.Manifest), &manifest); err != nil || manifest.Format != "qman-media-directory" || manifest.Version != 1 || manifest.ID != id || manifest.SourceID == "" || len(manifest.Entries) > media.MaxFiles {
				return errors.New("备份历史清单无效或不受支持")
			}
			if sourceID == "" {
				sourceID = manifest.SourceID
			} else if sourceID != manifest.SourceID {
				return errors.New("所选备份属于不同原来源，请分别清理；未删除任何文件")
			}
			for _, entry := range manifest.Entries {
				if err = ctx.Err(); err != nil {
					return err
				}
				item := entry.Item
				if item.InstanceID == "" {
					return errors.New("历史清单缺少原文件实例，不能自动清理")
				}
				if entry.Excluded == "" && !validBackupHash(entry.SHA256) {
					return errors.New("历史清单缺少有效内容摘要")
				}
				candidate, exists := candidates[item.InstanceID]
				if !exists {
					if len(order) >= media.MaxFiles {
						return errors.New("所选原文件超过上限，请分批清理")
					}
					candidate = &backedCandidate{item: item}
					candidates[item.InstanceID] = candidate
					order = append(order, item.InstanceID)
				} else if !sameBackupVersion(candidate.item, item) {
					candidate.conflict = true
				}
				if entry.Excluded != "" {
					if candidate.hash == "" {
						candidate.skip = "此项当时未备份成功，不按该备份清理"
					}
					continue
				}
				if candidate.hash != "" && candidate.hash != entry.SHA256 {
					candidate.conflict = true
				}
				candidate.hash = entry.SHA256
				candidate.skip = ""
			}
		}
		sources, err := s.catalog.Sources()
		if err != nil {
			return err
		}
		var saved *catalog.SavedSource
		for i := range sources {
			if sources[i].ID == sourceID {
				saved = &sources[i]
				break
			}
		}
		if saved == nil {
			return errors.New("这份备份没有本机登记的原来源关联；不会按账号名猜测，请到媒体页手动核对管理")
		}
		if _, err = media.Canonical(saved.Root); err != nil {
			return err
		}
		source, err := media.Resolve(saved.Root)
		if err != nil {
			return errors.New("原媒体目录不可访问，请先核实来源位置；不会清理其他副本")
		}
		source.ID = saved.ID
		source.Name = saved.Name
		if media.Overlap(source.Resolved, s.private) {
			return errors.New("原来源与应用数据重叠，拒绝清理")
		}
		items := []media.Item{}
		hashes := map[string]string{}
		skips := map[string]string{}
		for _, id := range order {
			if err = ctx.Err(); err != nil {
				return err
			}
			candidate := candidates[id]
			item := candidate.item
			reason := candidate.skip
			if candidate.conflict {
				reason = "不同备份对同一实例记录不一致，已跳过"
			} else if reason == "" {
				current, e := s.catalog.Item(source.ID, id)
				if e != nil {
					reason = "原实例已不在当前清单，可能已删除、替换或重新出现，已跳过"
				} else if !sameBackupVersion(item, current) {
					reason = "当前记录已不是备份时的原版本，已跳过"
				} else {
					item = current
				}
			}
			items = append(items, media.Bind(item, source))
			hashes[id] = candidate.hash
			if reason != "" {
				skips[id] = reason
			}
		}
		// Switching the displayed source is reversible and lets the existing execute
		// guard require exactly the original source shown in the confirmation.
		if err = s.activate(source, sources); err != nil {
			return err
		}
		s.mu.Lock()
		s.state.Phase = "preparing_delete"
		s.mu.Unlock()
		return s.prepareCleanup(ctx, source, media.Filter{}, items, hashes, skips, unique)
	})
}

func (s *Media) recordBackupSourceCleanup(job catalog.CleanupJob) error {
	if len(job.BackupIDs) == 0 {
		return nil
	}
	label := map[string]string{"preparing": "预检中，尚未删除", "prepared": "待确认，尚未删除", "completed": "任务结束", "partial": "部分完成", "interrupted": "任务中断，未自动继续"}[job.State]
	if label == "" {
		label = job.State
	}
	detail := fmt.Sprintf("%s；联合清单：回收 %d，跳过 %d，失败 %d，待核对 %d，未处理 %d", label, job.Recycled, job.Skipped, job.Failed, job.Uncertain, job.Ready+job.Pending)
	for _, id := range job.BackupIDs {
		row, err := s.catalog.Backup(id)
		if err != nil {
			return err
		}
		row.SourceCleanupJob = job.ID
		row.SourceCleanupDetail = detail
		if err = s.catalog.SaveBackup(row); err != nil {
			return err
		}
	}
	return nil
}
