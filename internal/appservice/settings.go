package appservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"strings"
)

type PathSettings struct {
	SourceDirectory string `json:"source_directory"`
	BackupDirectory string `json:"backup_directory"`
	ExportDirectory string `json:"export_directory"`
}

func (s *Media) Settings() (PathSettings, error) {
	var settings PathSettings
	payload, err := s.catalog.Preference("path_defaults")
	if err != nil {
		return settings, err
	}
	if payload != "" {
		if err = json.Unmarshal([]byte(payload), &settings); err != nil {
			return settings, errors.New("无法读取路径设置，请检查应用索引")
		}
	}
	return settings, nil
}

func settingsDirectory(path string, resolveAlias bool) (string, error) {
	if !filepath.IsAbs(path) || len(path) > 32767 {
		return "", errors.New("请输入完整的绝对目录路径，或使用选择文件夹")
	}
	if resolveAlias {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", errors.New("QQ 数据文件夹不可访问，请连接磁盘或重新选择")
		}
		path = resolved
	}
	canonical, err := media.Canonical(path)
	if err != nil {
		return "", errors.New("目录路径无法安全核实，请重新选择")
	}
	f, err := media.OpenRelative(canonical, ".")
	if err != nil {
		return "", errors.New("目录不可访问，请选择已存在的文件夹")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return "", errors.New("请选择已存在的文件夹")
	}
	return canonical, nil
}

func (s *Media) SaveSettings(settings PathSettings) (PathSettings, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return PathSettings{}, errors.New("请等待当前任务结束后保存设置")
	}
	return s.saveSettingsLocked(settings)
}

func (s *Media) saveSettingsLocked(settings PathSettings) (PathSettings, error) {
	previous, err := s.Settings()
	if err != nil {
		return PathSettings{}, err
	}
	settings.SourceDirectory = strings.TrimSpace(settings.SourceDirectory)
	settings.BackupDirectory = strings.TrimSpace(settings.BackupDirectory)
	settings.ExportDirectory = strings.TrimSpace(settings.ExportDirectory)
	// An unchanged, temporarily offline preference can remain saved while the
	// other field is edited or cleared. Every actual operation rechecks its path.
	if settings.SourceDirectory != "" && settings.SourceDirectory != previous.SourceDirectory {
		settings.SourceDirectory, err = settingsDirectory(settings.SourceDirectory, true)
		if err != nil {
			return PathSettings{}, err
		}
	}
	if settings.SourceDirectory != "" && media.Overlap(settings.SourceDirectory, s.private) {
		return PathSettings{}, errors.New("默认 QQ 数据文件夹必须与 Qman 应用数据分开")
	}
	for _, output := range []struct {
		path            *string
		previous, label string
	}{
		{&settings.BackupDirectory, previous.BackupDirectory, "默认备份保存目录"},
		{&settings.ExportDirectory, previous.ExportDirectory, "默认导出父目录"},
	} {
		if *output.path == "" {
			continue
		}
		if *output.path != output.previous {
			*output.path, err = settingsDirectory(*output.path, false)
			if err != nil {
				return PathSettings{}, err
			}
		}
		roots, e := s.protectedWithDefault(settings.SourceDirectory)
		if e != nil {
			return PathSettings{}, e
		}
		for _, root := range roots {
			if media.Overlap(root, *output.path) {
				return PathSettings{}, errors.New(output.label + "必须与 QQ 数据文件夹、已登记来源和 Qman 应用数据分开")
			}
		}
	}
	payload, err := json.Marshal(settings)
	if err != nil {
		return PathSettings{}, err
	}
	// Defaults publish together; a rejected field cannot partly save another.
	if err = s.catalog.SetPreference("path_defaults", string(payload)); err != nil {
		return PathSettings{}, err
	}
	return settings, nil
}

// Explicit directory selections fill an empty default. An existing default is
// never replaced by a one-off choice, including when that default is offline.
func (s *Media) RememberSourceDirectory(path string, restore bool) (string, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return "", errors.New("请等待当前任务结束后选择目录")
	}
	path, err := settingsDirectory(path, true)
	if err != nil {
		return "", err
	}
	if media.Overlap(path, s.private) {
		return "", errors.New("QQ 数据文件夹必须与 Qman 应用数据分开")
	}
	if restore && !strings.EqualFold(filepath.Base(path), "Tencent Files") {
		return "", errors.New("请选择最外层 Tencent Files 文件夹")
	}
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	if settings.SourceDirectory == "" {
		settings.SourceDirectory = path
		if _, err = s.saveSettingsLocked(settings); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (s *Media) RestoreDirectory() (string, string, error) {
	settings, err := s.Settings()
	if err != nil {
		return "", "", err
	}
	if settings.SourceDirectory == "" {
		return s.Preference("restore_directory"), "", nil
	}
	// A saved account/nt_data/media path may have a known Tencent Files ancestor.
	// Do not search siblings or guess a different account/root under a parent.
	for root := settings.SourceDirectory; ; root = filepath.Dir(root) {
		if strings.EqualFold(filepath.Base(root), "Tencent Files") {
			if _, err = media.Canonical(root); err != nil {
				return "", "", errors.New("默认 Tencent Files 路径发生变化，请在设置中重新选择")
			}
			return root, "已根据设置中的 QQ 数据文件夹填入目标 Tencent Files，请核对备份账号与目录后确认加载。", nil
		}
		if filepath.Dir(root) == root {
			return "", "默认 QQ 数据文件夹没有可确定的 Tencent Files 根，请选择目标文件夹。", nil
		}
	}
}

func (s *Media) BackupDirectory() (string, error) {
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	if settings.BackupDirectory != "" {
		return settings.BackupDirectory, nil
	}
	return s.catalog.Preference("backup_directory")
}

func (s *Media) ImportDefaultSource(ctx context.Context) (string, error) {
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	if settings.SourceDirectory == "" {
		return "", errors.New("请先在设置中保存默认 QQ 数据文件夹")
	}
	// Do not silently follow a new alias introduced after the explicit save.
	if _, err = media.Canonical(settings.SourceDirectory); err != nil {
		return "", errors.New("默认 QQ 数据文件夹路径已变化，请在设置中重新选择")
	}
	result, err := s.Discover(ctx, settings.SourceDirectory)
	return result.Root, err
}

// DirectoryDialogStart only supplies a usable picker starting point. It does
// not rewrite an offline saved default or silently change a backup destination.
func DirectoryDialogStart(paths ...string) string {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}
	return ""
}

func (s *Media) Preference(key string) string { v, _ := s.catalog.Preference(key); return v }
