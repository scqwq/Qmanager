// Package cloud123 implements the explicitly selected 123 web-session test flow.
// It does not read the QQ catalog, start scans, or delete local/remote files.
package cloud123

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ErrLogin = errors.New("云盘登录已失效，请重新登录")

type Status struct {
	LoggedIn   bool         `json:"logged_in"`
	Account    string       `json:"account"`
	NeedsCheck bool         `json:"needs_check"`
	Remembered bool         `json:"remembered"`
	Note       string       `json:"note"`
	Upload     UploadStatus `json:"upload"`
}
type UploadStatus struct {
	ID       string `json:"id"`
	Phase    string `json:"phase"`
	Name     string `json:"name"`
	Total    int64  `json:"total"`
	Done     int64  `json:"done"`
	Detail   string `json:"detail"`
	RemoteID string `json:"remote_id"`
}
type QR struct {
	ID        string    `json:"id"`
	Image     string    `json:"image"`
	ExpiresAt time.Time `json:"expires_at"`
}
type QRResult struct {
	State  string `json:"state"`
	Status Status `json:"status"`
}
type Folder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type FolderPage struct {
	Folders []Folder `json:"folders"`
	Next    string   `json:"next"`
	More    bool     `json:"more"`
}
type LocalFile struct {
	Ticket string `json:"ticket"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
}
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}
type remoteFile struct {
	ID      int64  `json:"FileId"`
	Name    string `json:"FileName"`
	Type    int    `json:"Type"`
	Size    int64  `json:"Size"`
	MD5     string `json:"Etag"`
	Parent  int64  `json:"ParentFileId"`
	Trashed bool   `json:"Trashed"`
}
type apiError struct {
	code    int
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s（业务码 %d）", safeMessage(e.message), e.code)
}
func (e *apiError) Is(target error) bool {
	return target == ErrLogin && (e.code == 20101 || e.code == 401 || e.code == 40101)
}

var secretPattern = regexp.MustCompile(`(?i)Bearer\s+[^\s"<>]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|https?://[^\s"<>]+|\b1[3-9]\d{9}\b|\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)

func safeMessage(s string) string {
	s = secretPattern.ReplaceAllString(s, "[已隐藏]")
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' {
			return -1
		}
		return r
	}, s)
	if len([]rune(s)) > 400 {
		s = string([]rune(s)[:400]) + "…"
	}
	if strings.TrimSpace(s) == "" {
		return "云盘请求未完成"
	}
	return s
}
