package appservice

import (
	"context"
	"errors"
	"fmt"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
)

func (s *Media) exportTarget(path string) (string, error) {
	target, err := settingsDirectory(path, false)
	if err != nil {
		return "", fmt.Errorf("导出父目录不可用，请重新选择：%w", err)
	}
	roots, err := s.protected()
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if media.Overlap(root, target) {
			return "", errors.New("导出父目录必须与 QQ 数据目录和 Qman 应用数据分开")
		}
	}
	return target, nil
}

func (s *Media) ExportSelected(parent context.Context, path string, request CleanupRequest) error {
	if _, err := s.exportTarget(path); err != nil {
		return err
	}
	return s.backupJob(parent, "exporting", func(ctx context.Context) error {
		source, err := s.currentMediaSource()
		if err != nil {
			return err
		}
		items, err := s.selectedMedia(ctx, source, request)
		if err != nil {
			return err
		}
		excluded, err := s.observeSelection(ctx, source, items, nil)
		if err != nil {
			return err
		}
		target, err := s.exportTarget(path)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.state.CheckStage = ""
		s.state.Scanned, s.state.Bytes = 0, 0
		s.mu.Unlock()
		result, err := mediabackup.ExportFlat(ctx, target, items, excluded, s.backupProgress)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.state.Message = fmt.Sprintf("已导出 %d 个文件，跳过 %d 项。保存位置：%s", result.Files, result.Excluded, result.Path)
		s.mu.Unlock()
		return nil
	})
}
