package appservice

import (
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"time"
)

// Only a reachable, readable exact parent permits acknowledging absence at a
// registered location. A missing drive/parent or denied access stays unknown.
func accessibleBackupParent(path string) error {
	parent := filepath.Dir(path)
	if _, err := media.Canonical(parent); err != nil {
		return errors.New("备份父目录不可访问或路径无法核实；未确认删除，请连接磁盘或重新定位")
	}
	f, err := media.OpenRelative(parent, ".")
	if err != nil {
		return errors.New("备份父目录不可访问；可能离线、已移动或权限不足，未确认删除")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return errors.New("备份父目录不可用，未确认删除")
	}
	return nil
}

func observeBackupLocation(row *catalog.BackupRecord) {
	now := time.Now().UTC()
	row.LocationCheckedAt = &now
	path := row.Path
	if row.DeletePath != "" {
		path = row.DeletePath
	}
	if _, err := media.Canonical(path); err != nil {
		row.LocalState, row.LocalDetail = "unavailable", "位置无法访问或含链接，未确认删除；请检查磁盘、权限或重新定位"
		return
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if accessibleBackupParent(path) != nil {
			row.LocalState, row.LocalDetail = "unavailable", "父目录不可访问；可能断盘、权限不足或父目录已移动，未确认删除"
			return
		}
		if row.DeletePath != "" {
			// A journalled quarantine can disappear while the original location
			// reappears. Require the normal identity checks before continuing.
			if _, e := os.Lstat(row.Path); e == nil {
				row.LocalState, row.LocalDetail = "pending", "删除暂存未找到，但原位置存在；继续删除前会重新校验"
				return
			} else if !os.IsNotExist(e) {
				row.LocalState, row.LocalDetail = "unavailable", "原位置无法核实，未确认删除"
				return
			}
		}
		row.LocalState, row.LocalDetail = "missing", "登记位置未找到，可能已删除、改名或移动；若需继续使用，请重新定位"
		return
	}
	if err != nil {
		row.LocalState, row.LocalDetail = "unavailable", "位置无法读取；请检查磁盘或权限，未确认删除"
		return
	}
	if row.Format == "directory" && !info.IsDir() || row.Format == "zip" && !info.Mode().IsRegular() {
		row.LocalState, row.LocalDetail = "changed", "位置对象类型与登记不符；操作前必须重新定位并校验"
		return
	}
	if row.DeletePath != "" {
		row.LocalState, row.LocalDetail = "pending", "删除暂存仍存在；可继续删除，执行前重新校验剩余成员"
		return
	}
	row.LocalState, row.LocalDetail = "present", "本地位置存在；此检查未校验包内文件完整性，加载或删除前仍会完整核验"
	if row.Status == "deleted" {
		row.LocalDetail += "；历史曾确认清理，当前位置再次出现，加载时将重新校验"
	}
}
