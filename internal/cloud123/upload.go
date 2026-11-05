package cloud123

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type uploadInit struct {
	Key         string      `json:"Key"`
	Bucket      string      `json:"Bucket"`
	ID          int64       `json:"FileId"`
	Reuse       bool        `json:"Reuse"`
	Info        *remoteFile `json:"Info"`
	UploadID    string      `json:"UploadId"`
	StorageNode string      `json:"StorageNode"`
	Slice       json.Number `json:"SliceSize"`
}

func (s *Service) StartUpload(parent, ticket string) error {
	if !s.operation.TryLock() {
		return errors.New("请等待当前云盘操作结束")
	}
	defer s.operation.Unlock()
	c, e := s.getClient()
	if e != nil {
		return e
	}
	id, e := parseID(parent)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeLocked() {
		return errors.New("已有上传正在进行")
	}
	if !s.folders[parent] {
		return errors.New("请重新选择云端目录")
	}
	chosen, ok := s.files[ticket]
	if !ok || time.Now().After(chosen.expires) {
		return errors.New("文件选择已过期，请重新选择文件")
	}
	delete(s.files, ticket)
	s.startUploadLocked(c, id, chosen.info.Name(), chosen.info.Size(), func(context.Context) (chosenFile, func(), error) {
		return chosen, nil, nil
	}, nil)
	return nil
}

type UploadReceipt struct {
	AccountID string
	FileID    string
	MD5       string
	Size      int64
}

// StartBackupUpload uses a registry-bound preparation callback. Only this path
// can upload an independently verified, app-generated temporary ZIP.
func (s *Service) StartBackupUpload(parent, name string, prepare func(context.Context) (string, os.FileInfo, func(), error), finish func(UploadReceipt, error) error) error {
	if !s.operation.TryLock() {
		return errors.New("请等待当前云盘操作结束")
	}
	defer s.operation.Unlock()
	c, err := s.getClient()
	if err != nil {
		return err
	}
	id, err := parseID(parent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeLocked() || s.client != c || !s.status.LoggedIn {
		return errors.New("云盘登录状态或上传任务已变化")
	}
	if !s.folders[parent] {
		return errors.New("请重新选择云端目录")
	}
	s.startUploadLocked(c, id, name, 0, func(ctx context.Context) (chosenFile, func(), error) {
		path, info, cleanup, e := prepare(ctx)
		if e == nil && (info == nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 100<<30 || !validName(info.Name())) {
			e = errors.New("备份 ZIP 名称或大小超出上传范围")
		}
		return chosenFile{path: path, info: info}, cleanup, e
	}, finish)
	return nil
}

func accountKey(uid string) string {
	sum := sha256.Sum256([]byte("qman/123-account/" + uid))
	return hex.EncodeToString(sum[:])
}

func (s *Service) CheckBackupObject(ctx context.Context, receipt UploadReceipt) (state, detail string) {
	c, err := s.getClient()
	if err != nil {
		return "unknown", "请登录上传时使用的账号后刷新"
	}
	s.mu.RLock()
	matches := s.client == c && accountKey(s.uid) == receipt.AccountID
	s.mu.RUnlock()
	if !matches {
		return "unknown", "当前账号与上传时的账号不同"
	}
	id, err := parseID(receipt.FileID)
	if err != nil || id <= 0 {
		return "unknown", "云端文件编号无法核实"
	}
	file, err := c.info(ctx, id)
	if err != nil {
		s.handle(c, err)
		var business *apiError
		if errors.As(err, &business) && (business.message == "文件不存在" || business.message == "该文件不存在") {
			return "missing", "云盘确认原文件不存在"
		}
		return "unknown", safeMessage(err.Error())
	}
	s.mu.RLock()
	matches = s.status.LoggedIn && s.client == c && accountKey(s.uid) == receipt.AccountID
	s.mu.RUnlock()
	if !matches {
		return "unknown", "核对期间云盘登录状态发生变化"
	}
	if file.Trashed {
		return "missing", "原文件已移入云盘回收站"
	}
	if file.Type != 0 || file.Size != receipt.Size || !strings.EqualFold(file.MD5, receipt.MD5) {
		return "changed", "云端对象的大小或 MD5 与上传记录不同"
	}
	return "present", "云端文件编号、大小与 MD5 核对一致"
}

func (s *Service) startUploadLocked(c *Client, id int64, name string, total int64, prepare func(context.Context) (chosenFile, func(), error), finish func(UploadReceipt, error) error) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	s.status.Upload = UploadStatus{ID: randomID(), Phase: "checking", Name: name, Total: total}
	account := accountKey(s.uid)
	go func() {
		defer close(done)
		defer cancel()
		chosen, cleanup, e := prepare(ctx)
		if cleanup != nil {
			defer cleanup()
		}
		var file remoteFile
		if e == nil {
			s.mu.Lock()
			s.status.Upload.Total = chosen.info.Size()
			s.status.Upload.Name = chosen.info.Name()
			s.mu.Unlock()
			file, e = s.upload(ctx, c, id, chosen)
		}
		if finish != nil {
			receipt := UploadReceipt{AccountID: account, FileID: strconv.FormatInt(file.ID, 10), MD5: file.MD5, Size: file.Size}
			e = errors.Join(e, finish(receipt, e))
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cancel = nil
		if e != nil {
			s.status.Upload.Phase = "failed"
			s.status.Upload.Detail = safeMessage(e.Error())
			if errors.Is(e, context.Canceled) {
				s.status.Upload.Phase = "cancelled"
				s.status.Upload.Detail = "上传已取消；已发送的云端内容不会自动删除"
			}
			if errors.Is(e, ErrLogin) {
				s.status.LoggedIn = false
				s.status.Remembered = false
				s.status.NeedsCheck = false
				c.setToken("")
				_ = os.Remove(s.credential)
				s.status.Note = ErrLogin.Error()
			}
			return
		}
		s.status.Upload.Phase = "complete"
		s.status.Upload.Name = file.Name
		s.status.Upload.Done = file.Size
		s.status.Upload.RemoteID = strconv.FormatInt(file.ID, 10)
		s.status.Upload.Detail = "上传完成，大小和MD5核对一致"
	}()
}
func (s *Service) CancelUpload() {
	s.mu.RLock()
	cancel := s.cancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}
func (s *Service) progress(phase string, done int64) {
	s.mu.Lock()
	s.status.Upload.Phase = phase
	s.status.Upload.Done = done
	s.mu.Unlock()
}

type progressReader struct {
	r      io.Reader
	ctx    context.Context
	update func(int64)
	done   int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	if e := p.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := p.r.Read(b)
	p.done += int64(n)
	p.update(p.done)
	return n, e
}

// Refuse existing names before initialization. The test page does not overwrite
// objects, and does not assume undocumented collision-enum semantics.
func (c *Client) ensureNameAbsent(ctx context.Context, parent int64, name string) error {
	next := "0"
	for page := 0; page < 100; page++ {
		r, e := c.call(ctx, false, http.MethodGet, "/b/api/file/list/new", mapValues(parent, next), nil)
		if e != nil {
			return e
		}
		var d struct {
			Next  string       `json:"Next"`
			Files []remoteFile `json:"InfoList"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			return errors.New("无法核对云端同名对象")
		}
		for _, f := range d.Files {
			if !f.Trashed && strings.EqualFold(f.Name, name) {
				return errors.New("目标目录已有同名对象，请使用不同名称的测试文件")
			}
		}
		if d.Next == "" || d.Next == "-1" {
			return nil
		}
		if d.Next == next {
			return errors.New("目录核对分页未推进")
		}
		next = d.Next
	}
	return errors.New("目标目录过大，未完成同名核对，请选择较小的测试目录")
}
func (s *Service) upload(ctx context.Context, c *Client, parent int64, chosen chosenFile) (remoteFile, error) {
	f, e := os.Open(chosen.path)
	if e != nil {
		return remoteFile{}, errors.New("无法读取所选文件")
	}
	defer f.Close()
	if !sameFile(f, chosen.path, chosen.info) {
		return remoteFile{}, errors.New("所选文件已变化，请重新选择")
	}
	if e = c.folder(ctx, parent); e != nil {
		return remoteFile{}, e
	}
	if e = c.ensureNameAbsent(ctx, parent, chosen.info.Name()); e != nil {
		return remoteFile{}, e
	}
	h := md5.New()
	pr := &progressReader{r: f, ctx: ctx, update: func(n int64) { s.progress("checking", n) }}
	if _, e = io.CopyBuffer(h, pr, make([]byte, 256<<10)); e != nil {
		return remoteFile{}, e
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if !sameFile(f, chosen.path, chosen.info) {
		return remoteFile{}, errors.New("内容检查期间文件发生变化，未开始上传")
	}
	s.progress("initializing", 0)
	r, e := c.post(ctx, "/b/api/file/upload_request", map[string]any{"driveId": 0, "etag": digest, "fileName": chosen.info.Name(), "parentFileId": parent, "size": chosen.info.Size(), "type": 0, "duplicate": 1, "RequestSource": nil})
	if e != nil {
		return remoteFile{}, e
	}
	var init uploadInit
	if json.Unmarshal(r.Data, &init) != nil {
		return remoteFile{}, errors.New("上传初始化响应不完整")
	}
	finalID := int64(0)
	if init.Reuse {
		if init.Info != nil {
			finalID = init.Info.ID
		} else {
			finalID = init.ID
		}
	} else {
		partSize, e := init.Slice.Int64()
		if e != nil || partSize < 256<<10 || partSize > 256<<20 || init.ID <= 0 || init.Key == "" || init.Bucket == "" || init.UploadID == "" || init.StorageNode == "" {
			return remoteFile{}, errors.New("上传初始化参数无法确认，未发送正文")
		}
		parts := (chosen.info.Size() + partSize - 1) / partSize
		if parts > 10000 {
			return remoteFile{}, errors.New("分片数量超过测试页限制")
		}
		for part := int64(1); part <= parts; part++ {
			if !sameFile(f, chosen.path, chosen.info) {
				return remoteFile{}, errors.New("上传期间文件发生变化，已停止；云端部分内容需核对")
			}
			auth, e := c.post(ctx, "/b/api/file/s3_upload_object/auth", map[string]any{"bucket": init.Bucket, "key": init.Key, "partNumberStart": part, "partNumberEnd": part + 1, "uploadId": init.UploadID, "StorageNode": init.StorageNode})
			if e != nil {
				return remoteFile{}, e
			}
			var urls struct {
				URLs map[string]string `json:"presignedUrls"`
			}
			if json.Unmarshal(auth.Data, &urls) != nil {
				return remoteFile{}, errors.New("分片授权响应无法识别")
			}
			signed := urls.URLs[strconv.FormatInt(part, 10)]
			u, e := trustedObjectURL(signed)
			if e != nil {
				return remoteFile{}, e
			}
			offset := (part - 1) * partSize
			length := min(partSize, chosen.info.Size()-offset)
			reader := &progressReader{r: io.NewSectionReader(f, offset, length), ctx: ctx, update: func(n int64) { s.progress("uploading", offset+n) }}
			req, e := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), reader)
			if e != nil {
				return remoteFile{}, errors.New("无法准备分片上传")
			}
			req.ContentLength = length
			req.Header.Set("Origin", "https://yun.123pan.cn")
			req.Header.Set("Referer", "https://yun.123pan.cn/")
			req.Header.Set("User-Agent", userAgent)
			putClient := *c.http
			putClient.Jar = nil
			putClient.Timeout = 3 * time.Minute
			resp, e := putClient.Do(req)
			if e != nil {
				if ctx.Err() != nil {
					return remoteFile{}, ctx.Err()
				}
				return remoteFile{}, errors.New("文件传输未完成，请检查网络；本次任务不会自动重试")
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
			_ = resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return remoteFile{}, fmt.Errorf("分片传输未完成（HTTP %d），云端部分内容需核对", resp.StatusCode)
			}
		}
		if !sameFile(f, chosen.path, chosen.info) {
			return remoteFile{}, errors.New("文件已变化，未提交完成登记；云端内容需核对")
		}
		s.progress("completing", chosen.info.Size())
		complete, e := c.post(ctx, "/b/api/file/upload_complete/v2", map[string]any{"fileId": init.ID, "bucket": init.Bucket, "fileSize": chosen.info.Size(), "key": init.Key, "isMultipart": parts > 1, "uploadId": init.UploadID, "StorageNode": init.StorageNode})
		if e != nil {
			return remoteFile{}, e
		}
		var d struct {
			File *remoteFile `json:"file_info"`
		}
		if json.Unmarshal(complete.Data, &d) != nil || d.File == nil {
			return remoteFile{}, errors.New("完成登记结果尚未确认，请到云盘核对，避免重复上传")
		}
		finalID = d.File.ID
	}
	if finalID <= 0 {
		return remoteFile{}, errors.New("未取得最终文件编号，请到云盘核对")
	}
	s.progress("verifying", chosen.info.Size())
	file, e := c.info(ctx, finalID)
	if e != nil {
		return remoteFile{}, e
	}
	if file.Trashed || file.Type != 0 || file.Parent != parent || file.Name != chosen.info.Name() || file.Size != chosen.info.Size() || !strings.EqualFold(file.MD5, digest) {
		return remoteFile{}, errors.New("云端对象的名称、位置、大小或MD5核对不一致，请检查云盘")
	}
	if !sameFile(f, chosen.path, chosen.info) {
		return remoteFile{}, errors.New("本地文件已经变化，云端结果需重新核对")
	}
	return file, nil
}
