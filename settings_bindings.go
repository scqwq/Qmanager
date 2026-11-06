package main

import (
	"fmt"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"qqarchive/internal/appservice"
	"qqarchive/internal/diagnostics"
)

func (a *App) GetSettings() (appservice.PathSettings, error) { return a.media.Settings() }
func (a *App) GetDiagnostics() (diagnostics.Snapshot, error) { return diagnostics.Read() }
func (a *App) EditDiagnosticRecord(kind, id string) error {
	return diagnostics.EditRecord(kind, id)
}
func (a *App) GetAppearance() (appservice.AppearanceSettings, error) { return a.media.Appearance() }
func (a *App) SaveAppearance(settings appservice.AppearanceSettings) (appservice.AppearanceSettings, error) {
	return a.media.SaveAppearance(settings)
}
func (a *App) DiscardAppearancePreview() { a.media.DiscardAppearancePreview() }
func (a *App) ChooseAppearanceImage() (appservice.AppearanceImage, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择背景图片", Filters: []runtime.FileFilter{{DisplayName: "静态 PNG / JPEG 图片", Pattern: "*.png;*.jpg;*.jpeg"}}})
	if err != nil || path == "" {
		return appservice.AppearanceImage{}, err
	}
	return a.media.ImportAppearanceImage(path)
}
func (a *App) SaveSettings(settings appservice.PathSettings) (appservice.PathSettings, error) {
	return a.media.SaveSettings(settings)
}
func (a *App) ImportDefaultSource() (string, error) { return a.media.ImportDefaultSource(a.ctx) }
func (a *App) SearchQQDirectory(id, hint, backupHint string) (appservice.DirectorySearchResult, error) {
	return a.media.SearchQQDirectory(a.ctx, id, hint, backupHint)
}
func (a *App) CancelQQDirectorySearch(id string) { a.media.CancelQQDirectorySearch(id) }
func (a *App) ChooseSettingsDirectory(kind, current string) (string, error) {
	settings, err := a.media.Settings()
	if err != nil {
		return "", err
	}
	options := runtime.OpenDialogOptions{}
	switch kind {
	case "source":
		options.Title = "选择默认 QQ 数据文件夹（如最外层 Tencent Files）"
		options.DefaultDirectory = appservice.DirectoryDialogStart(current, settings.SourceDirectory)
	case "export":
		options.Title = "选择默认导出父目录"
		options.CanCreateDirectories = true
		options.DefaultDirectory = appservice.DirectoryDialogStart(current, settings.ExportDirectory)
	case "backup":
		options.Title = "选择默认备份保存目录（可新建文件夹）"
		options.CanCreateDirectories = true
		options.DefaultDirectory = appservice.DirectoryDialogStart(current, settings.BackupDirectory, a.media.Preference("backup_directory"))
	default:
		return "", fmt.Errorf("未知目录设置类型")
	}
	return runtime.OpenDirectoryDialog(a.ctx, options)
}

func (a *App) ExportIndex() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择应用记录备份位置（不含媒体文件）"})
	if err != nil || path == "" {
		return "", err
	}
	return a.media.ExportIndex(a.ctx, path)
}
