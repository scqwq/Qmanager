package main

import (
	"crypto/rand"
	"fmt"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"qqarchive/internal/appservice"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/storage"
	"time"
)

type backupPick struct {
	ticket  string
	row     catalog.BackupRecord
	expires time.Time
}

func (a *App) GetEstimate(f media.Filter) (mediabackup.Preview, error) {
	return a.media.BackupEstimate(f)
}

func (a *App) OpenBackupLocation(id string) error {
	path, err := a.media.BackupLocation(id)
	if err != nil {
		return err
	}
	return storage.OpenFolder(path)
}
func (a *App) CreateBackup(f media.Filter) error {
	return a.CreateBackupWithFormat(f, "directory")
}
func (a *App) CreateBackupWithFormat(f media.Filter, format string) error {
	path, err := a.ChooseBackupDestination()
	if err != nil || path == "" {
		return err
	}
	return a.media.BackupFormat(a.ctx, path, f, format)
}

func (a *App) GetSelectedBackupEstimate(request appservice.CleanupRequest) (mediabackup.Preview, error) {
	return a.media.SelectedBackupEstimate(a.ctx, request)
}

func (a *App) CreateSelectedBackup(request appservice.CleanupRequest, format, path string) error {
	return a.media.BackupSelected(a.ctx, path, request, format)
}

func (a *App) ExportSelectedMedia(request appservice.CleanupRequest) error {
	path, err := a.ChooseExportDestination()
	if err != nil || path == "" {
		return err
	}
	return a.ExportSelectedMediaAt(request, path)
}

func (a *App) ChooseExportDestination() (string, error) {
	settings, err := a.media.Settings()
	if err != nil || settings.ExportDirectory != "" {
		return settings.ExportDirectory, err
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择导出父目录", CanCreateDirectories: true})
}
func (a *App) ExportSelectedMediaAt(request appservice.CleanupRequest, path string) error {
	return a.media.ExportSelected(a.ctx, path, request)
}
func (a *App) GetBackupDirectory() (string, error) { return a.media.BackupDirectory() }
func (a *App) ChooseBackupDestination() (string, error) {
	defaultPath, err := a.media.BackupDirectory()
	if err != nil {
		return "", err
	}
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择备份保存目录（可新建文件夹，复制并保留源文件）", DefaultDirectory: appservice.DirectoryDialogStart(defaultPath, a.media.Preference("backup_directory")), CanCreateDirectories: true})
	if err != nil || path == "" {
		return "", err
	}
	return a.media.RememberBackupDirectory(path)
}
func (a *App) CreateBackupAt(f media.Filter, format, path string) error {
	return a.media.BackupFormat(a.ctx, path, f, format)
}
func (a *App) dialogDirectory(key string) string {
	if key == "backup_directory" {
		path, err := a.media.BackupDirectory()
		if err != nil {
			return ""
		}
		return appservice.DirectoryDialogStart(path, a.media.Preference(key))
	}
	path := a.media.Preference(key)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return ""
}
func (a *App) GetBackupHistory() ([]catalog.BackupRecord, error) {
	rows, err := a.media.Backups()
	for i := range rows {
		rows[i].Display = publicBackupDisplay(rows[i].Display)
	}
	return rows, err
}
func (a *App) CheckBackupOriginals(id string) error { return a.media.CheckBackupOriginals(a.ctx, id) }
func (a *App) ChooseBackup(expectedID string) (*appservice.BackupPreview, error) {
	choice, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.QuestionDialog, Title: "加载备份", Message: "请选择备份保存形式", Buttons: []string{"备份文件夹", "ZIP 文件", "取消"}, DefaultButton: "备份文件夹", CancelButton: "取消"})
	if err != nil || choice == "取消" || choice == "" {
		return nil, err
	}
	var path string
	if choice == "ZIP 文件" {
		path, err = runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 Qman ZIP 备份", Filters: []runtime.FileFilter{{DisplayName: "Qman ZIP 备份", Pattern: "*.zip"}}, DefaultDirectory: a.dialogDirectory("backup_directory")})
	} else {
		path, err = runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择含 backup.json、.qman 和媒体目录树的备份目录", DefaultDirectory: a.dialogDirectory("backup_directory")})
	}
	if err != nil || path == "" {
		return nil, err
	}
	preview, err := a.media.PreviewExternalBackup(a.ctx, path, expectedID)
	if err != nil {
		return nil, err
	}
	if expectedID != "" && preview.Record.ID != expectedID {
		return nil, fmt.Errorf("所选备份编号与历史不符，未替换原记录")
	}
	preview.Ticket = rand.Text()
	row := preview.Record
	row.Manifest = ""
	a.backupPickMu.Lock()
	a.backupPick = &backupPick{ticket: preview.Ticket, row: row, expires: time.Now().Add(15 * time.Minute)}
	a.backupPickMu.Unlock()
	return &preview, nil
}
func (a *App) PreviewHistoryBackup(id string) (appservice.BackupPreview, error) {
	return a.media.PreviewHistoryBackup(a.ctx, id)
}
func (a *App) ChooseRestoreTarget() (string, error) {
	target, _, err := a.media.RestoreDirectory()
	if err != nil {
		return "", err
	}
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择最外层 Tencent Files（按备份内账号路径补回）", DefaultDirectory: appservice.DirectoryDialogStart(target, a.media.Preference("restore_directory"))})
	if err != nil || path == "" {
		return "", err
	}
	return a.media.RememberSourceDirectory(path, true)
}
func (a *App) LoadBackup(id, target string) error { return a.media.RestoreBackup(a.ctx, id, target) }
func (a *App) LoadSelectedBackup(ticket, target string) error {
	a.backupPickMu.Lock()
	pick := a.backupPick
	if pick == nil || pick.ticket != ticket || time.Now().After(pick.expires) {
		a.backupPickMu.Unlock()
		return fmt.Errorf("所选备份已过期，请重新选择")
	}
	a.backupPick = nil
	a.backupPickMu.Unlock()
	return a.media.RestoreExternalBackup(a.ctx, pick.row, target)
}
func (a *App) ViewLoadedBackup(id string) error    { return a.media.ViewLoadedBackup(a.ctx, id) }
func (a *App) DeleteBackups(ids []string) error    { return a.media.DeleteBackups(a.ctx, ids) }
func (a *App) DeleteBackupHistory(id string) error { return a.media.DeleteBackupHistory(a.ctx, id) }

func (a *App) OpenBackup() error {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择含 backup.json、.qman 和媒体目录树的备份目录"})
	if err != nil || path == "" {
		return err
	}
	return a.media.OpenBackup(a.ctx, path)
}
