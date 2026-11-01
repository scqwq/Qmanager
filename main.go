package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"qqarchive/internal/appservice"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/storage"
)

//go:embed all:frontend/dist
var assets embed.FS

type App struct {
	ctx     context.Context
	media   *appservice.Media
	private string
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.media.RecoverCleanup(); err != nil {
		log.Print("未能核对上次媒体删除任务，请检查私有索引")
	}
	if os.Getenv("QMAN_SMOKE_CACHE_ONLY") == "1" {
		return
	}
	if source := os.Getenv("QMAN_SMOKE_SOURCE"); source != "" {
		if _, err := a.media.Select(source); err == nil {
			a.media.StartUpdate(ctx, "fast")
		}
	}
}

// UIReady is inert in normal use. An explicitly supplied synthetic smoke source
// enables a private local probe of the real Vue/WebView2/Go binding and scan.
func (a *App) UIReady() {
	if os.Getenv("QMAN_SMOKE_SOURCE") == "" && os.Getenv("QMAN_SMOKE_CACHE_ONLY") != "1" {
		return
	}
	s := a.media.State()
	if (s.Phase == "ready" || (s.Cached && os.Getenv("QMAN_SMOKE_CACHE_ONLY") == "1")) && s.Report != nil {
		w, h := runtime.WindowGetSize(a.ctx)
		defaultDates := a.media.Page(media.Filter{View: "observed"})
		withInitial := a.media.Page(media.Filter{View: "observed", IncludeBaseline: true})
		dateMatches := 0
		if len(withInitial.Items) > 0 {
			day := withInitial.Items[0].FirstSeenAt.Local().Format("2006-01-02")
			dateMatches = a.media.Page(media.Filter{View: "observed", IncludeBaseline: true, From: day, To: day}).Total
		}
		b, _ := json.Marshal(map[string]interface{}{"update_kind": s.Report.UpdateKind, "known_skipped": s.Report.KnownSkipped, "inspected": s.Report.Inspected, "files": s.Report.Files, "complete": s.Report.Complete, "cached": s.Cached, "width": w, "height": h, "bridge": "vue-webview2-go", "observed_default": defaultDates.Total, "observed_with_initial": withInitial.Total, "initial_date_matches": dateMatches})
		if os.WriteFile(filepath.Join(a.private, "ui-smoke.json"), b, 0600) == nil {
			runtime.Quit(a.ctx)
		}
	}
}
func (a *App) shutdown(context.Context) { a.media.Close() }
func (a *App) ChooseSource() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 Tencent Files、账号目录或 nt_data（自动发现账号媒体）", CanCreateDirectories: false})
	if err != nil || path == "" {
		return "", err
	}
	result, err := a.media.Discover(a.ctx, path)
	return result.Root, err
}
func (a *App) StartScan() error                  { return a.media.Start(a.ctx) }
func (a *App) StartUpdate(mode string) error     { return a.media.StartUpdate(a.ctx, mode) }
func (a *App) CancelScan()                       { a.media.Cancel() }
func (a *App) GetState() appservice.State        { return a.media.State() }
func (a *App) GetPage(f media.Filter) media.Page { return a.media.Page(f) }
func (a *App) SwitchSource(id string) error      { return a.media.Switch(id) }
func (a *App) RenameSource(name string) error    { return a.media.Rename(name) }
func (a *App) StopWatch()                        { a.media.Stop() }
func (a *App) StopUpdate() error                 { return a.media.StopUpdate() }
func (a *App) GetEstimate(f media.Filter) (mediabackup.Preview, error) {
	return a.media.BackupEstimate(f)
}
func (a *App) GetMediaURL(id string) (string, error)           { return a.media.MediaURL(id) }
func (a *App) GetHistory(id string) ([]catalog.History, error) { return a.media.History(id) }
func (a *App) GetBuckets(view string) []media.Bucket           { return a.media.Buckets(view) }
func (a *App) GetFilterBuckets(f media.Filter) []media.Bucket {
	return a.media.Buckets(f.View, f.IncludeBaseline)
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
func (a *App) GetBackupDirectory() string { return a.dialogDirectory("backup_directory") }
func (a *App) ChooseBackupDestination() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择备份保存目录（可新建文件夹，复制并保留源文件）", DefaultDirectory: a.dialogDirectory("backup_directory"), CanCreateDirectories: true})
	if err != nil || path == "" {
		return "", err
	}
	return a.media.RememberBackupDirectory(path)
}
func (a *App) CreateBackupAt(f media.Filter, format, path string) error {
	return a.media.BackupFormat(a.ctx, path, f, format)
}
func (a *App) dialogDirectory(key string) string {
	path := a.media.Preference(key)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return ""
}
func (a *App) GetBackupHistory() ([]catalog.BackupRecord, error) { return a.media.Backups() }
func (a *App) ChooseBackup(expectedID string) (*appservice.BackupPreview, error) {
	choice, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{Type: runtime.QuestionDialog, Title: "加载备份", Message: "请选择备份保存形式", Buttons: []string{"备份文件夹", "ZIP 文件", "取消"}, DefaultButton: "备份文件夹", CancelButton: "取消"})
	if err != nil || choice == "取消" || choice == "" {
		return nil, err
	}
	var path string
	if choice == "ZIP 文件" {
		path, err = runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择 Qman ZIP 备份", Filters: []runtime.FileFilter{{DisplayName: "Qman ZIP 备份", Pattern: "*.zip"}}, DefaultDirectory: a.dialogDirectory("backup_directory")})
	} else {
		path, err = runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择含 manifest.json 的备份目录", DefaultDirectory: a.dialogDirectory("backup_directory")})
	}
	if err != nil || path == "" {
		return nil, err
	}
	preview, err := a.media.PreviewBackup(a.ctx, path, expectedID)
	if err != nil {
		return nil, err
	}
	if expectedID != "" && preview.Record.ID != expectedID {
		return nil, fmt.Errorf("所选备份编号与历史不符，未替换原记录")
	}
	return &preview, nil
}
func (a *App) PreviewHistoryBackup(id string) (appservice.BackupPreview, error) {
	return a.media.PreviewHistoryBackup(a.ctx, id)
}
func (a *App) ChooseRestoreTarget() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择最外层 Tencent Files（按备份内账号路径补回）", DefaultDirectory: a.dialogDirectory("restore_directory")})
}
func (a *App) LoadBackup(id, target string) error { return a.media.RestoreBackup(a.ctx, id, target) }
func (a *App) ViewLoadedBackup(id string) error   { return a.media.ViewLoadedBackup(a.ctx, id) }
func (a *App) DeleteBackups(ids []string) error   { return a.media.DeleteBackups(a.ctx, ids) }
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
func (a *App) OpenBackup() error {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择含 manifest.json 的 Qman 媒体备份目录"})
	if err != nil || path == "" {
		return err
	}
	return a.media.OpenBackup(a.ctx, path)
}
func (a *App) ExportIndex() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择索引备份位置（含观察历史，不含媒体）"})
	if err != nil || path == "" {
		return "", err
	}
	return a.media.ExportIndex(a.ctx, path)
}

func main() {
	private := os.Getenv("QMAN_DATA_DIR")
	if private == "" {
		private = filepath.Join(os.Getenv("LOCALAPPDATA"), "Qman")
	}
	private, err := media.Canonical(private)
	if err != nil {
		log.Fatal("应用数据路径不可用")
	}
	if err = storage.Private(private); err != nil {
		log.Fatal("无法建立私有应用数据目录：", err)
	}
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		log.Fatal("无法打开应用索引：", err)
	}
	service, err := appservice.NewMedia(c, private)
	if err != nil {
		c.Close()
		log.Fatal("无法启动本地媒体服务：", err)
	}
	a := &App{media: service, private: private}
	instanceID := "3b82bd1a-7e2d-4ced-9a9c-e02a16328016"
	if os.Getenv("QMAN_SMOKE_SOURCE") != "" || os.Getenv("QMAN_SMOKE_CACHE_ONLY") == "1" {
		sum := sha256.Sum256([]byte(private))
		instanceID = fmt.Sprintf("qman-smoke-%x", sum[:12])
	}
	if err := wails.Run(&options.App{
		Title: "Qman · QQ 媒体管理", Width: 1280, Height: 860, MinWidth: 960, MinHeight: 660,
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 250, A: 255},
		AssetServer: &assetserver.Options{Assets: assets, Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if len(r.URL.Path) >= 7 && r.URL.Path[:7] == "/media/" {
					a.media.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r)
			})
		}},
		OnStartup: a.startup, OnShutdown: a.shutdown, Bind: []interface{}{a},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: instanceID, OnSecondInstanceLaunch: func(options.SecondInstanceData) { runtime.WindowShow(a.ctx); runtime.WindowUnminimise(a.ctx) }},
		Windows:            &windows.Options{WebviewUserDataPath: filepath.Join(private, "webview")},
	}); err != nil {
		log.Fatal("桌面程序启动失败：", err)
	}
}
