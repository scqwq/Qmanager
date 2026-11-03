package appservice

import (
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
)

// BackupLocation accepts a registered ID, never a frontend-supplied disk path.
// Opening a location does not verify package contents or alter its saved status.
func (s *Media) BackupLocation(id string) (string, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return "", errors.New("请等待当前任务结束后打开备份位置")
	}
	row, err := s.catalog.Backup(id)
	if err != nil {
		return "", errors.New("未找到这条备份记录，请刷新备份历史")
	}
	return backupLocationDirectory(row)
}

func backupLocationDirectory(row catalog.BackupRecord) (string, error) {
	if row.Status == "deleting" || row.DeletePath != "" {
		return "", errors.New("备份包删除尚未结束，请先核对该任务结果")
	}
	if !filepath.IsAbs(row.Path) {
		return "", errors.New("备份保存位置无效，请重新定位")
	}
	location, err := media.Canonical(row.Path)
	if err != nil {
		return "", errors.New("备份保存位置无法安全访问，请重新定位")
	}
	info, err := os.Stat(location)
	if err != nil {
		return "", errors.New("备份保存位置不存在或无法访问，请连接磁盘或重新定位")
	}
	switch row.Format {
	case "directory":
		if info.IsDir() {
			return location, nil
		}
	case "zip":
		if info.Mode().IsRegular() {
			return filepath.Dir(location), nil
		}
	}
	return "", errors.New("备份位置类型与登记不符，请重新定位")
}
