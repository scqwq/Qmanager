package cloud123

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxResponse = 2 << 20
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type Client struct {
	http      *http.Client
	api       string
	login     string
	mu        sync.RWMutex
	token     string
	loginUUID string
	trace     *responseTrace
}

func newClient(trace *responseTrace, uuid string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{http: &http.Client{Jar: jar, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, api: "https://api.123panapi.com", login: "https://user.123pan.cn", loginUUID: uuid, trace: trace}
}
func (c *Client) setToken(token string) { c.mu.Lock(); c.token = token; c.mu.Unlock() }
func (c *Client) getToken() string      { c.mu.RLock(); defer c.mu.RUnlock(); return c.token }
func (c *Client) call(ctx context.Context, login bool, method, path string, query url.Values, body any) (envelope, error) {
	base := c.api
	if login {
		base = c.login
	}
	u := base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return envelope{}, errors.New("无法准备云盘请求")
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return envelope{}, errors.New("云盘接口地址无效")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://yun.123pan.cn")
	req.Header.Set("Referer", "https://yun.123pan.cn/")
	req.Header.Set("platform", "web")
	req.Header.Set("app-version", "3")
	if body != nil {
		req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	}
	if login {
		req.Header.Set("Origin", c.login)
		req.Header.Set("Referer", c.login+"/centerlogin")
		req.Header.Set("app-version", "132")
		req.Header.Set("LoginUuid", c.loginUUID)
	} else {
		token := c.getToken()
		if token == "" {
			return envelope{}, ErrLogin
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if login || path == "/b/api/user/info" {
			c.trace.record(path, 0, []byte(`{"message":"请求没有取得HTTP响应","error":"network_or_cancelled"}`))
		}
		if ctx.Err() != nil {
			return envelope{}, ctx.Err()
		}
		return envelope{}, errors.New("无法连接123云盘，请检查网络后重试")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(b) > maxResponse {
		return envelope{}, errors.New("云盘响应不完整或超过大小限制")
	}
	if login || path == "/b/api/user/info" {
		c.trace.record(path, resp.StatusCode, b)
	}
	if resp.StatusCode == 401 {
		return envelope{}, ErrLogin
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return envelope{}, fmt.Errorf("云盘请求未完成（HTTP %d）", resp.StatusCode)
	}
	var result envelope
	if json.Unmarshal(b, &result) != nil {
		return envelope{}, errors.New("云盘返回了无法识别的响应")
	}
	if result.Code != 0 && result.Code != 200 {
		return result, &apiError{result.Code, result.Message}
	}
	return result, nil
}
func (c *Client) post(ctx context.Context, path string, body any) (envelope, error) {
	return c.call(ctx, false, http.MethodPost, path, nil, body)
}
func (c *Client) info(ctx context.Context, id int64) (remoteFile, error) {
	r, e := c.post(ctx, "/b/api/file/info", map[string]any{"fileIdList": []any{map[string]any{"fileId": id}}})
	if e != nil {
		return remoteFile{}, e
	}
	var data struct {
		Files []remoteFile `json:"infoList"`
	}
	if json.Unmarshal(r.Data, &data) != nil || len(data.Files) != 1 || data.Files[0].ID != id {
		return remoteFile{}, errors.New("云端对象无法确认，请重新选择目录")
	}
	return data.Files[0], nil
}
func (c *Client) user(ctx context.Context) (string, string, error) {
	r, e := c.call(ctx, false, http.MethodGet, "/b/api/user/info", nil, nil)
	if e != nil {
		return "", "", e
	}
	var d struct {
		ID       int64       `json:"UID"`
		Passport json.Number `json:"Passport"`
		Mail     string      `json:"Mail"`
	}
	if json.Unmarshal(r.Data, &d) != nil || d.ID <= 0 {
		return "", "", errors.New("云盘账号信息不完整")
	}
	account := "已登录"
	p := d.Passport.String()
	if len(p) >= 7 {
		account = p[:3] + "****" + p[len(p)-4:]
	} else if i := strings.Index(d.Mail, "@"); i > 0 {
		account = d.Mail[:1] + "***" + d.Mail[i:]
	}
	return strconv.FormatInt(d.ID, 10), account, nil
}
func parseID(s string) (int64, error) {
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil || v < 0 {
		return 0, errors.New("云端目录编号无效")
	}
	return v, nil
}
func (c *Client) folder(ctx context.Context, id int64) error {
	if id == 0 {
		return nil
	}
	f, e := c.info(ctx, id)
	if e != nil {
		return e
	}
	if f.Type != 1 || f.Trashed {
		return errors.New("目标不是可用的云端文件夹，请重新选择")
	}
	return nil
}
func (c *Client) folders(ctx context.Context, id int64, next string) (FolderPage, error) {
	if next == "" {
		next = "0"
	}
	if len(next) > 80 {
		return FolderPage{}, errors.New("目录分页标记无效")
	}
	q := url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {next}, "orderBy": {"file_name"}, "orderDirection": {"asc"}, "parentFileId": {strconv.FormatInt(id, 10)}, "trashed": {"false"}, "SearchData": {""}, "Page": {"1"}, "OnlyLookAbnormalFile": {"0"}, "event": {"homeListFile"}, "operateType": {"1"}, "inDirectSpace": {"false"}, "fileCategory": {"0"}, "isSearchOrder": {"false"}, "fileTypes[]": {"0"}}
	r, e := c.call(ctx, false, http.MethodGet, "/b/api/file/list/new", q, nil)
	if e != nil {
		return FolderPage{}, e
	}
	var d struct {
		Next  string       `json:"Next"`
		Files []remoteFile `json:"InfoList"`
	}
	if json.Unmarshal(r.Data, &d) != nil {
		return FolderPage{}, errors.New("目录列表格式无法识别")
	}
	p := FolderPage{Folders: []Folder{}, Next: d.Next, More: d.Next != "" && d.Next != "-1"}
	for _, f := range d.Files {
		if f.ID > 0 && f.Type == 1 && !f.Trashed {
			p.Folders = append(p.Folders, Folder{strconv.FormatInt(f.ID, 10), f.Name})
		}
	}
	if p.More && p.Next == next {
		return FolderPage{}, errors.New("云盘分页未推进，请刷新目录")
	}
	return p, nil
}
func mapValues(parent int64, next string) url.Values {
	return url.Values{"driveId": {"0"}, "limit": {"100"}, "next": {next}, "orderBy": {"update_time"}, "orderDirection": {"desc"}, "parentFileId": {strconv.FormatInt(parent, 10)}, "trashed": {"false"}, "SearchData": {""}, "Page": {"1"}, "OnlyLookAbnormalFile": {"0"}, "event": {"homeListFile"}, "operateType": {"1"}, "inDirectSpace": {"false"}, "fileCategory": {"0"}, "isSearchOrder": {"false"}, "fileTypes[]": {"0"}}
}
func validName(name string) bool {
	return strings.TrimSpace(name) == name && name != "" && name != "." && name != ".." && len([]rune(name)) < 255 && !strings.ContainsAny(name, "\\/:*?|<>\"\x00\r\n")
}
func (c *Client) mkdir(ctx context.Context, parent int64, name string) (Folder, error) {
	if !validName(name) {
		return Folder{}, errors.New("请输入有效的文件夹名称，不能包含路径或特殊字符")
	}
	if e := c.folder(ctx, parent); e != nil {
		return Folder{}, e
	}
	if e := c.ensureNameAbsent(ctx, parent, name); e != nil {
		return Folder{}, e
	}
	r, e := c.post(ctx, "/b/api/file/upload_request", map[string]any{"driveId": 0, "etag": "", "fileName": name, "parentFileId": parent, "size": 0, "type": 1, "duplicate": 1, "NotReuse": true, "RequestSource": nil})
	if e != nil {
		return Folder{}, e
	}
	var d struct {
		Info *remoteFile `json:"Info"`
	}
	if json.Unmarshal(r.Data, &d) != nil || d.Info == nil || d.Info.ID <= 0 || d.Info.Type != 1 {
		return Folder{}, errors.New("目录创建响应不完整，请刷新目录核对")
	}
	return Folder{strconv.FormatInt(d.Info.ID, 10), d.Info.Name}, nil
}

func trustedObjectURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("云盘返回了不安全的上传地址")
	}
	h := strings.ToLower(u.Hostname())
	allowed := false
	for _, suffix := range []string{"123pan.com", "123pan.cn", "123panapi.com", "123278.com"} {
		if h == suffix || strings.HasSuffix(h, "."+suffix) {
			allowed = true
		}
	}
	if !allowed {
		return nil, errors.New("云盘返回了未识别的上传主机，未发送文件")
	}
	return u, nil
}
