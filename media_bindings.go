package main

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"qqarchive/internal/appservice"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
)

func (a *App) ChooseSource() (string, error) {
	settings, err := a.media.Settings()
	if err != nil {
		return "", err
	}
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 Tencent Files、账号目录或 nt_data（自动发现账号媒体）", DefaultDirectory: appservice.DirectoryDialogStart(settings.SourceDirectory), CanCreateDirectories: false})
	if err != nil || path == "" {
		return "", err
	}
	path, err = a.media.RememberSourceDirectory(path, false)
	if err != nil {
		return "", err
	}
	result, err := a.media.Discover(a.ctx, path)
	return result.Root, err
}
func (a *App) StartScan() error              { return a.media.Start(a.ctx) }
func (a *App) StartUpdate(mode string) error { return a.media.StartUpdate(a.ctx, mode) }
func (a *App) CancelOperation(phase string, revision int) string {
	return a.media.CancelOperation(phase, revision)
}
func (a *App) OperationReminder(operation, path string, confirm bool) (string, error) {
	return a.media.OperationReminder(operation, path, confirm)
}
func (a *App) CancelScan()                       { a.media.Cancel() }
func (a *App) GetState() appservice.State        { return a.media.State() }
func (a *App) GetPage(f media.Filter) media.Page { return a.media.Page(f) }
func (a *App) SwitchSource(id string) error      { return a.media.Switch(id) }
func (a *App) RenameSource(name string) error    { return a.media.Rename(name) }
func (a *App) StopWatch()                        { a.media.Stop() }
func (a *App) StopUpdate() error                 { return a.media.StopUpdate() }

func (a *App) GetMediaURL(id string) (string, error)           { return a.media.MediaURL(id) }
func (a *App) GetHistory(id string) ([]catalog.History, error) { return a.media.History(id) }
func (a *App) GetBuckets(view string) []media.Bucket           { return a.media.Buckets(view) }
func (a *App) GetFilterBuckets(f media.Filter) []media.Bucket {
	return a.media.FilterBuckets(f)
}

func (a *App) PrepareMediaDelete(request appservice.CleanupRequest) error {
	return a.media.PrepareMediaDelete(a.ctx, request)
}
func (a *App) PrepareBackupSourceDelete(ids []string) error {
	return a.media.PrepareBackupSourceDelete(a.ctx, ids)
}
func (a *App) ExecuteMediaDelete(id string) error                      { return a.media.ExecuteMediaDelete(a.ctx, id) }
func (a *App) GetMediaDeleteJob(id string) (catalog.CleanupJob, error) { return a.media.CleanupJob(id) }
func (a *App) GetMediaDeleteItems(id string, offset, limit int) ([]catalog.CleanupItem, error) {
	return a.media.CleanupItems(id, offset, limit)
}
