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
	"qqarchive/internal/cloud123"
	"qqarchive/internal/diagnostics"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"strings"
	"sync"
)

//go:embed all:frontend/dist
var assets embed.FS

type App struct {
	ctx          context.Context
	media        *appservice.Media
	private      string
	cloud        *cloud123.Service
	backupPickMu sync.Mutex
	backupPick   *backupPick
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	startDiagnostics(a.private)
	if err := a.media.RecoverCleanup(); err != nil {
		log.Print("未能核对上次媒体删除任务，请检查私有索引")
		diagnostics.Record(ctx, "error", "cleanup.recovery_failed", "上次任务状态未能核对，请检查应用索引")
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
		diag, diagErr := a.GetDiagnostics()
		b, _ := json.Marshal(map[string]interface{}{"update_kind": s.Report.UpdateKind, "known_skipped": s.Report.KnownSkipped, "inspected": s.Report.Inspected, "files": s.Report.Files, "complete": s.Report.Complete, "cached": s.Cached, "width": w, "height": h, "bridge": "vue-webview2-go", "observed_default": defaultDates.Total, "observed_with_initial": withInitial.Total, "initial_date_matches": dateMatches, "diagnostics_ready": diagErr == nil && len(diag.Entries) > 0 && diag.Directory == filepath.Join(a.private, "logs")})
		if os.WriteFile(filepath.Join(a.private, "ui-smoke.json"), b, 0600) == nil {
			runtime.Quit(a.ctx)
		}
	}
}
func (a *App) shutdown(context.Context) {
	if a.cloud != nil {
		a.cloud.Close()
	}
	a.media.Close()
}

// Wails calls startup only after acquiring its single-instance lock. Opening
// crash/log files earlier could interfere with a running instance's handles.
func startDiagnostics(private string) {
	logDir := filepath.Join(private, "logs")
	if info, e := os.Lstat(logDir); e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		log.Print("日志目录类型异常，未启用日志")
	} else if e = storage.Private(logDir); e != nil {
		log.Print("无法建立私有日志目录")
	} else if logger, e := diagnostics.Open(logDir); e != nil {
		log.Print("无法启动本地诊断日志")
	} else {
		diagnostics.SetDefault(logger)
		if e = logger.EnableCrashOutput(); e != nil {
			logger.Record(context.Background(), "warning", "crash.capture_unavailable", "Go 崩溃详情记录未能启用")
		}
		logger.Record(context.Background(), "info", "app.start", "Qman 0.9.2 / diagnostics-1 启动")
	}
}
func main() {
	defer func() {
		if logger := diagnostics.Default(); logger != nil {
			_ = logger.Close()
		}
	}()
	private := os.Getenv("QMAN_DATA_DIR")
	if private == "" && cloudResearchLog != "" && cloudResearchDataDir != "" {
		private = cloudResearchDataDir
	}
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
	cloud, err := cloud123.New(private, cloudResearchLog)
	if err != nil {
		service.Close()
		log.Fatal("无法启动云盘测试服务：", err)
	}
	a := &App{media: service, private: private, cloud: cloud}
	instanceID := "3b82bd1a-7e2d-4ced-9a9c-e02a16328016"
	cloudTestProfile := cloudResearchLog != "" && cloudResearchDataDir != "" && strings.EqualFold(filepath.Clean(private), filepath.Clean(cloudResearchDataDir))
	if os.Getenv("QMAN_SMOKE_SOURCE") != "" || os.Getenv("QMAN_SMOKE_CACHE_ONLY") == "1" || cloudTestProfile {
		sum := sha256.Sum256([]byte(private))
		instanceID = fmt.Sprintf("qman-smoke-%x", sum[:12])
	}
	if err := wails.Run(&options.App{
		Title: "Qman · QQ 媒体管理", Width: 1280, Height: 860, MinWidth: 960, MinHeight: 660,
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 250, A: 255},
		AssetServer: &assetserver.Options{Assets: assets, Middleware: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/cloud-qr/") {
					a.cloud.ServeQR(w, r)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/appearance/") {
					a.media.ServeAppearance(w, r)
					return
				}
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
	diagnostics.Record(context.Background(), "info", "app.exit", "应用正常结束")
}
