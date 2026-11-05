package cloud123

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"qqarchive/internal/media"
	"qqarchive/internal/storage"
)

type qrSession struct {
	public     QR
	uniID      string
	client     *Client
	generation uint64
	lastPoll   time.Time
	image      []byte
}
type chosenFile struct {
	path    string
	info    os.FileInfo
	expires time.Time
}
type savedSession struct {
	Token   string `json:"token"`
	UID     string `json:"uid"`
	Account string `json:"account"`
}
type credentialSaveError struct {
	stage string
	cause error
}

func (e *credentialSaveError) Error() string {
	return "无法保存加密登录凭据（" + e.stage + "），请重试"
}
func (e *credentialSaveError) Unwrap() error { return e.cause }

type Service struct {
	mu                  sync.RWMutex
	authGate            sync.Mutex
	operation           sync.Mutex
	client              *Client
	loginFactory        func() *Client
	private, credential string
	trace               *responseTrace
	status              Status
	uid                 string
	generation          uint64
	qr                  *qrSession
	folders             map[string]bool
	files               map[string]chosenFile
	cancel              context.CancelFunc
	done                chan struct{}
	closed              bool
}

func randomID() string { return rand.Text() }
func (s *Service) newLoginClient() *Client {
	if s.loginFactory != nil {
		return s.loginFactory()
	}
	return newClient(s.trace, uuid.NewString())
}
func New(private, tracePath string) (*Service, error) {
	dir := filepath.Join(private, "cloud123")
	if info, e := os.Lstat(dir); e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("云盘凭据目录不安全")
	}
	if e := storage.Private(dir); e != nil {
		return nil, errors.New("无法建立云盘私有凭据目录")
	}
	s := &Service{private: private, credential: filepath.Join(dir, "session.dpapi"), trace: &responseTrace{path: tracePath}, folders: map[string]bool{"0": true}, files: map[string]chosenFile{}}
	s.client = newClient(s.trace, randomID())
	if f, e := os.Lstat(s.credential); e == nil {
		if !f.Mode().IsRegular() || f.Size() > 65536 {
			s.status.Note = "已保存的云盘凭据无法读取，请重新登录"
			return s, nil
		}
		b, e := os.ReadFile(s.credential)
		if e == nil {
			b, e = unprotect(b)
		}
		var saved savedSession
		if e == nil {
			e = json.Unmarshal(b, &saved)
		}
		if e == nil && saved.Token != "" && len(saved.Token) < 16384 {
			s.client.setToken(saved.Token)
			s.uid = saved.UID
			s.status.Account = saved.Account
			s.status.NeedsCheck = true
			s.status.Remembered = true
		} else {
			s.status.Note = "已保存的云盘凭据无法解密，请重新登录"
		}
	}
	return s, nil
}
func (s *Service) Status() Status     { s.mu.RLock(); defer s.mu.RUnlock(); return s.status }
func (s *Service) activeLocked() bool { return s.cancel != nil }
func (s *Service) accept(ctx context.Context, c *Client, token string, g uint64) error {
	if len(token) < 10 || len(token) > 16384 {
		return errors.New("云盘登录凭据格式无效")
	}
	c.setToken(token)
	uid, account, e := c.user(ctx)
	if e != nil {
		c.setToken("")
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.generation != g {
		return errors.New("本次登录已取消")
	}
	saved := savedSession{Token: token, UID: uid, Account: account}
	if e = s.save(saved); e != nil {
		return e
	}
	s.client = c
	s.uid = uid
	s.qr = nil
	s.files = map[string]chosenFile{}
	s.folders = map[string]bool{"0": true}
	s.status = Status{LoggedIn: true, Account: account, Remembered: true}
	return nil
}
func (s *Service) save(v savedSession) error {
	b, e := json.Marshal(v)
	if e != nil {
		return errors.New("无法准备登录凭据")
	}
	b, e = protect(b)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.credential), "session-*.tmp")
	if e != nil {
		return errors.New("无法保存加密登录凭据")
	}
	name := f.Name()
	defer os.Remove(name)
	stage := "写入"
	_, e = f.Write(b)
	if e == nil {
		stage = "同步"
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		stage = "发布"
		e = replaceCredential(name, s.credential)
	}
	if e != nil {
		return &credentialSaveError{stage, e}
	}
	return nil
}
func (s *Service) getClient() (*Client, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errors.New("云盘服务已关闭")
	}
	if !s.status.LoggedIn {
		return nil, ErrLogin
	}
	return s.client, nil
}
func (s *Service) handle(c *Client, e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, ErrLogin) {
		s.mu.Lock()
		if s.client == c {
			if s.cancel != nil {
				s.cancel()
			}
			s.status.LoggedIn = false
			s.status.NeedsCheck = false
			s.status.Note = ErrLogin.Error()
			s.client.setToken("")
			s.qr = nil
			s.generation++
			s.folders = map[string]bool{"0": true}
			s.files = map[string]chosenFile{}
			_ = os.Remove(s.credential)
			s.status.Remembered = false
		}
		s.mu.Unlock()
		return ErrLogin
	}
	return e
}
func (s *Service) Refresh(ctx context.Context) (Status, error) {
	if !s.authGate.TryLock() {
		return s.Status(), errors.New("正在处理登录，请稍候")
	}
	defer s.authGate.Unlock()
	s.mu.RLock()
	c := s.client
	expected := s.uid
	s.mu.RUnlock()
	if c.getToken() == "" {
		return s.Status(), nil
	}
	uid, account, e := c.user(ctx)
	if e != nil {
		return s.Status(), s.handle(c, e)
	}
	if expected != "" && uid != expected {
		return s.Status(), s.handle(c, ErrLogin)
	}
	s.mu.Lock()
	if s.client == c {
		s.uid = uid
		s.status.Account = account
		s.status.LoggedIn = true
		s.status.NeedsCheck = false
		s.status.Note = ""
	}
	st := s.status
	s.mu.Unlock()
	return st, nil
}
func (s *Service) Logout() error {
	if !s.authGate.TryLock() {
		return errors.New("正在处理登录，请稍候")
	}
	defer s.authGate.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeLocked() {
		return errors.New("请先取消或等待上传结束")
	}
	if e := os.Remove(s.credential); e != nil && !os.IsNotExist(e) {
		return errors.New("无法移除本机保存的登录凭据，请重试")
	}
	s.client.setToken("")
	s.generation++
	s.qr = nil
	s.uid = ""
	s.status = Status{}
	s.folders = map[string]bool{"0": true}
	s.files = map[string]chosenFile{}
	return nil
}
func (s *Service) Folders(ctx context.Context, parent, next string) (FolderPage, error) {
	c, e := s.getClient()
	if e != nil {
		return FolderPage{}, e
	}
	s.mu.RLock()
	known := s.folders[parent]
	s.mu.RUnlock()
	if !known {
		return FolderPage{}, errors.New("请从根目录重新选择文件夹")
	}
	id, e := parseID(parent)
	if e != nil {
		return FolderPage{}, e
	}
	p, e := c.folders(ctx, id, next)
	if e != nil {
		return FolderPage{}, s.handle(c, e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != c {
		return FolderPage{}, ErrLogin
	}
	for _, f := range p.Folders {
		if len(s.folders) < 20000 {
			s.folders[f.ID] = true
		}
	}
	return p, nil
}
func (s *Service) Mkdir(ctx context.Context, parent, name string) (Folder, error) {
	if !s.operation.TryLock() {
		return Folder{}, errors.New("请等待当前云盘操作结束")
	}
	defer s.operation.Unlock()
	c, e := s.getClient()
	if e != nil {
		return Folder{}, e
	}
	s.mu.RLock()
	known := s.folders[parent]
	active := s.activeLocked()
	s.mu.RUnlock()
	if active {
		return Folder{}, errors.New("请等待上传结束")
	}
	if !known {
		return Folder{}, errors.New("请重新选择云端目录")
	}
	id, e := parseID(parent)
	if e != nil {
		return Folder{}, e
	}
	f, e := c.mkdir(ctx, id, name)
	if e != nil {
		return Folder{}, s.handle(c, e)
	}
	s.mu.Lock()
	if s.client == c {
		s.folders[f.ID] = true
	}
	s.mu.Unlock()
	return f, nil
}
func (s *Service) SelectFile(path string) (LocalFile, error) {
	c, e := s.getClient()
	if e != nil {
		return LocalFile{}, e
	}
	_ = c
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return LocalFile{}, errors.New("请选择可读的普通文件，不支持目录或链接")
	}
	resolved, e := media.Canonical(path)
	if e != nil || media.Overlap(resolved, s.private) {
		return LocalFile{}, errors.New("不能上传应用私有数据目录中的文件")
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		if standard, err := media.Canonical(filepath.Join(base, "Qman")); err == nil && media.Overlap(resolved, standard) {
			return LocalFile{}, errors.New("不能上传Qman私有数据目录中的文件")
		}
	}
	if info.Size() <= 0 {
		return LocalFile{}, errors.New("暂不支持上传空文件")
	}
	if info.Size() > 100<<30 {
		return LocalFile{}, errors.New("测试页单文件大小上限为100 GiB")
	}
	if !validName(info.Name()) {
		return LocalFile{}, errors.New("文件名不受云盘支持，请在本机另存测试副本后选择")
	}
	t := randomID()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeLocked() {
		return LocalFile{}, errors.New("请等待当前上传结束")
	}
	s.files = map[string]chosenFile{t: {path: path, info: info, expires: time.Now().Add(15 * time.Minute)}}
	return LocalFile{t, info.Name(), info.Size()}, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.qr = nil
	s.generation++
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}
func sameFile(f *os.File, path string, before os.FileInfo) bool {
	a, e := f.Stat()
	if e != nil {
		return false
	}
	b, e := os.Lstat(path)
	if e != nil || !b.Mode().IsRegular() {
		return false
	}
	return os.SameFile(a, before) && os.SameFile(a, b) && a.Size() == before.Size() && b.Size() == before.Size() && a.ModTime().Equal(before.ModTime()) && b.ModTime().Equal(before.ModTime())
}
func readCredentialLimit(f *os.File) ([]byte, error) { return io.ReadAll(io.LimitReader(f, 65537)) }
