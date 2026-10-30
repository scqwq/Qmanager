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
	if os.Getenv("QMAN_SMOKE_CACHE_ONLY") == "1" {
		return
	}
	if source := os.Getenv("QMAN_SMOKE_SOURCE"); source != "" {
		if _, err := a.media.Select(source); err == nil {
			a.media.Start(ctx)
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
		b, _ := json.Marshal(map[string]interface{}{"files": s.Report.Files, "complete": s.Report.Complete, "cached": s.Cached, "width": w, "height": h, "bridge": "vue-webview2-go"})
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
func (a *App) StartScan() error                                { return a.media.Start(a.ctx) }
func (a *App) CancelScan()                                     { a.media.Cancel() }
func (a *App) GetState() appservice.State                      { return a.media.State() }
func (a *App) GetPage(f media.Filter) media.Page               { return a.media.Page(f) }
func (a *App) SwitchSource(id string) error                    { return a.media.Switch(id) }
func (a *App) RenameSource(name string) error                  { return a.media.Rename(name) }
func (a *App) StopWatch()                                      { a.media.Stop() }
func (a *App) GetEstimate(f media.Filter) mediabackup.Preview  { return a.media.Estimate(f) }
func (a *App) GetMediaURL(id string) (string, error)           { return a.media.MediaURL(id) }
func (a *App) GetHistory(id string) ([]catalog.History, error) { return a.media.History(id) }
func (a *App) GetBuckets(view string) []media.Bucket           { return a.media.Buckets(view) }
func (a *App) CreateBackup(f media.Filter) error {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择媒体备份存放位置（复制，不移动源文件）"})
	if err != nil || path == "" {
		return err
	}
	return a.media.Backup(a.ctx, path, f)
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
