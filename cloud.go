package main

import (
	"errors"

	"qqarchive/internal/cloud123"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Empty in normal builds. The dedicated local test build may set this to an
// existing, explicitly selected research log via -ldflags; never a token path.
var cloudResearchLog string
var cloudResearchDataDir string

func (a *App) GetCloudStatus() cloud123.Status                      { return a.cloud.Status() }
func (a *App) RefreshCloudLogin() (cloud123.Status, error)          { return a.cloud.Refresh(a.ctx) }
func (a *App) GetCloudQRCode() (cloud123.QR, error)                 { return a.cloud.NewQR(a.ctx) }
func (a *App) PollCloudQRCode(id string) (cloud123.QRResult, error) { return a.cloud.PollQR(a.ctx, id) }
func (a *App) CancelCloudQRCode()                                   { a.cloud.CancelQR() }
func (a *App) LoginCloudPassword(account, password string) error {
	return a.cloud.PasswordLogin(a.ctx, account, password)
}
func (a *App) LogoutCloud() error { return a.cloud.Logout() }
func (a *App) ListCloudFolders(parent, next string) (cloud123.FolderPage, error) {
	return a.cloud.Folders(a.ctx, parent, next)
}
func (a *App) CreateCloudFolder(parent, name string) (cloud123.Folder, error) {
	return a.cloud.Mkdir(a.ctx, parent, name)
}
func (a *App) ChooseCloudFile() (cloud123.LocalFile, error) {
	if !a.cloud.Status().LoggedIn {
		return cloud123.LocalFile{}, errors.New("请先登录云盘")
	}
	path, e := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择用于云盘上传测试的文件"})
	if e != nil || path == "" {
		return cloud123.LocalFile{}, e
	}
	return a.cloud.SelectFile(path)
}
func (a *App) StartCloudUpload(parent, ticket string) error {
	return a.cloud.StartUpload(parent, ticket)
}
func (a *App) CancelCloudUpload() { a.cloud.CancelUpload() }
