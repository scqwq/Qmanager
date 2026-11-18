package cloud123

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"qqarchive/internal/media"
)

const (
	folderBatchSize  = 950
	folderEntryLimit = 20000
)

type LocalFolder struct {
	Ticket  string `json:"ticket"`
	Name    string `json:"name"`
	Files   int    `json:"files"`
	Batches int    `json:"batches"`
	Issues  int    `json:"issues"`
}

type folderChoice struct {
	job     folderJob
	account string
	expires time.Time
}

func (s *Service) selectedFolder(path string) (string, string, error) {
	root, err := media.Canonical(path)
	if err != nil || filepath.Dir(root) == root || !validName(filepath.Base(root)) {
		return "", "", errors.New("请选择名称有效的普通文件夹")
	}
	if media.Overlap(root, s.private) {
		return "", "", errors.New("不能上传包含 Qman 私有数据的目录")
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" && media.Overlap(root, filepath.Join(base, "Qman")) {
		return "", "", errors.New("不能上传包含 Qman 私有数据的目录")
	}
	f, err := os.Open(root)
	if err != nil {
		return "", "", errors.New("无法打开所选文件夹")
	}
	defer f.Close()
	info, err := f.Stat()
	id := media.FileIdentity(f)
	if err != nil || !info.IsDir() || (runtime.GOOS == "windows" && id == "") {
		return "", "", errors.New("无法确认所选文件夹")
	}
	return root, id, nil
}

func (s *Service) SelectFolder(ctx context.Context, path string) (LocalFolder, error) {
	s.mu.RLock()
	account := accountKey(s.uid)
	ready := s.status.LoggedIn && !s.activeLocked() && !s.closed
	s.mu.RUnlock()
	if !ready {
		return LocalFolder{}, errors.New("请先登录并等待当前上传结束")
	}
	root, identity, err := s.selectedFolder(path)
	if err != nil {
		return LocalFolder{}, err
	}
	job := folderJob{ID: randomID(), Root: root, RootIdentity: identity, Name: filepath.Base(root)}
	files := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return errors.New("有目录无法完整读取，请检查权限后重新选择")
		}
		if len(job.Entries) >= folderEntryLimit {
			return errors.New("试用版每次最多处理 20000 个文件及目录")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(rel) || len(rel) > 8192 || strings.Count(rel, string(filepath.Separator)) >= 64 {
			return errors.New("所选目录的路径超出试用版处理范围")
		}
		e := folderEntry{Path: rel, Directory: entry.IsDir(), State: "pending"}
		if !e.Directory {
			files++
			e.Batch = (files-1)/folderBatchSize + 1
		}
		switch {
		case !validName(entry.Name()):
			e.State, e.Detail = "failed", "名称不受云盘支持"
		case e.Directory:
			if _, err = media.Canonical(path); err != nil {
				e.State, e.Detail = "failed", "目录为链接或无法核实"
			}
		default:
			e.snapshot(root)
		}
		job.Entries = append(job.Entries, e)
		if e.Directory && e.State == "failed" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return LocalFolder{}, err
	}
	if len(job.Entries) == 0 || job.Entries[0].State != "pending" {
		return LocalFolder{}, errors.New("所选文件夹无法核实，请重新选择")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.status.LoggedIn || s.activeLocked() || s.closed || accountKey(s.uid) != account {
		return LocalFolder{}, errors.New("云盘账号或上传状态已变化，请重新选择")
	}
	s.folderChoice = folderChoice{job, account, time.Now().Add(15 * time.Minute)}
	summary := folderSummary(job)
	return LocalFolder{Ticket: job.ID, Name: job.Name, Files: summary.Total, Batches: summary.Batches, Issues: summary.Issues}, nil
}

func (e *folderEntry) snapshot(root string) {
	f, err := media.OpenRelative(root, e.Path)
	if err != nil {
		e.State, e.Detail = "failed", "文件为链接或无法读取"
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 100<<30 {
		e.State, e.Detail = "failed", "需要可读且大于 0 字节的普通文件，单文件上限 100 GiB"
		return
	}
	e.Size, e.Modified, e.Identity = info.Size(), info.ModTime().UnixNano(), media.FileIdentity(f)
	if runtime.GOOS == "windows" && e.Identity == "" {
		e.State, e.Detail = "failed", "无法确认本地文件身份"
	}
}

func (s *Service) FolderUploadStatus() (FolderUploadStatus, error) {
	s.mu.RLock()
	account := accountKey(s.uid)
	loggedIn, cachedAccount, cached := s.status.LoggedIn, s.folderAccount, s.folderStatus
	s.mu.RUnlock()
	if !loggedIn {
		return FolderUploadStatus{}, nil
	}
	if cachedAccount == account {
		return cached, nil
	}
	db, err := s.openFolderStore(account, false)
	if os.IsNotExist(err) {
		return FolderUploadStatus{}, nil
	}
	if err != nil {
		return FolderUploadStatus{}, errors.New("无法读取文件夹上传记录")
	}
	defer db.Close()
	job, err := readFolderJob(db)
	if errors.Is(err, sql.ErrNoRows) {
		return FolderUploadStatus{}, nil
	}
	if err != nil {
		return FolderUploadStatus{}, errors.New("无法读取文件夹上传记录")
	}
	return folderSummary(job), nil
}

func (s *Service) FolderUploadIssues(id string, offset int) ([]FolderUploadIssue, error) {
	if offset < 0 || offset >= folderEntryLimit {
		return nil, errors.New("结果页码无效")
	}
	s.mu.RLock()
	account, loggedIn := accountKey(s.uid), s.status.LoggedIn
	s.mu.RUnlock()
	if !loggedIn {
		return nil, ErrLogin
	}
	db, err := s.openFolderStore(account, false)
	if err != nil {
		return nil, errors.New("无法读取上传结果")
	}
	defer db.Close()
	rows, err := db.Query(`SELECT e.path,e.state,e.detail FROM folder_entries e
JOIN folder_job j ON j.id=? WHERE e.state IN ('failed','skipped') ORDER BY e.seq LIMIT 50 OFFSET ?`, id, offset)
	if err != nil {
		return nil, errors.New("无法读取上传结果")
	}
	defer rows.Close()
	issues := []FolderUploadIssue{}
	for rows.Next() {
		var issue FolderUploadIssue
		if err := rows.Scan(&issue.Path, &issue.State, &issue.Detail); err != nil {
			return nil, errors.New("无法读取上传结果")
		}
		issues = append(issues, issue)
	}
	if rows.Err() != nil {
		return nil, errors.New("无法读取上传结果")
	}
	return issues, nil
}

func (s *Service) StartFolderUpload(parent, ticket string) error {
	return s.changeFolderJob(parent, ticket, "start")
}

func (s *Service) ResumeFolderUpload(id string) error {
	return s.changeFolderJob("", id, "resume")
}

func (s *Service) AbandonFolderUpload(id string) error {
	return s.changeFolderJob("", id, "abandon")
}

func (s *Service) changeFolderJob(parent, id, action string) error {
	if !s.authGate.TryLock() {
		return errors.New("请等待当前登录操作结束")
	}
	defer s.authGate.Unlock()
	if !s.operation.TryLock() {
		return errors.New("请等待当前云盘操作结束")
	}
	defer s.operation.Unlock()
	s.mu.RLock()
	c, account := s.client, accountKey(s.uid)
	choice, known := s.folderChoice, s.folders[parent]
	loggedIn, busy := s.status.LoggedIn, s.activeLocked() || s.closed
	s.mu.RUnlock()
	if !loggedIn {
		return ErrLogin
	}
	if busy {
		return errors.New("请等待当前上传结束")
	}
	db, err := s.openFolderStore(account, action == "start")
	if err != nil {
		return errors.New("无法打开文件夹上传记录")
	}
	closeDB := true
	defer func() {
		if closeDB {
			db.Close()
		}
	}()
	job, err := readFolderJob(db)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return errors.New("无法读取文件夹上传记录")
	}
	canResume := job.Phase == "running" || job.Phase == "paused"
	if action == "start" {
		if canResume {
			return errors.New("请先继续或结束上一次文件夹上传")
		}
		if !known || choice.account != account || choice.job.ID != id || time.Now().After(choice.expires) {
			return errors.New("选择已过期，请重新选择文件夹和云端目录")
		}
		target, err := parseID(parent)
		if err != nil {
			return err
		}
		job = choice.job
		job.Parent, job.Phase = target, "running"
		if err := saveFolderJob(db, job); err != nil {
			return errors.New("无法保存文件夹上传清单")
		}
	} else if !canResume || job.ID != id {
		return errors.New("此上传任务已结束，请刷新结果")
	}
	if action == "abandon" {
		job.Phase, job.Detail = "abandoned", "任务已结束，已上传文件保留在云端"
		if _, err := db.Exec("UPDATE folder_job SET phase=?,detail=?", job.Phase, job.Detail); err != nil {
			return errors.New("无法更新文件夹上传记录")
		}
		s.setFolderStatus(account, folderSummary(job))
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.activeLocked() || s.client != c || !s.status.LoggedIn {
		return errors.New("云盘状态已变化，可以稍后继续此任务")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done
	s.folderChoice = folderChoice{}
	s.folderAccount, s.folderStatus = account, folderSummary(job)
	s.folderStatus.Phase, s.folderStatus.Detail = "running", ""
	closeDB = false
	go s.runFolderUpload(ctx, c, db, account, job, done)
	return nil
}

func (s *Service) setFolderStatus(account string, status FolderUploadStatus) {
	s.mu.Lock()
	s.folderAccount, s.folderStatus = account, status
	s.mu.Unlock()
}

func (s *Service) runFolderUpload(ctx context.Context, c *Client, db *sql.DB, account string, job folderJob, done chan struct{}) {
	defer close(done)
	defer db.Close()
	status := folderSummary(job)
	status.Phase, status.Detail = "running", ""
	err := s.uploadFolderEntries(context.WithValue(ctx, folderUploadRequest{}, true), c, db, account, &job, &status)
	status.Phase, status.Detail = "complete", ""
	if err != nil {
		status.Phase, status.Detail = "paused", safeMessage(err.Error())
		if errors.Is(err, context.Canceled) {
			status.Detail = "已暂停，可以继续上传剩余文件"
		}
	}
	if _, saveErr := db.Exec("UPDATE folder_job SET phase=?,detail=?", status.Phase, status.Detail); saveErr != nil {
		status.Phase, status.Detail = "paused", "无法保存任务状态，请稍后继续核对"
	}
	if errors.Is(err, ErrLogin) {
		s.handle(c, err)
	}
	s.mu.Lock()
	s.cancel()
	s.cancel = nil
	s.folderAccount, s.folderStatus = account, status
	s.mu.Unlock()
}
