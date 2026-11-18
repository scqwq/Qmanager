package cloud123

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"qqarchive/internal/media"
)

type BackupFolderReceipt struct {
	AccountID     string
	FolderID      string
	Objects       []UploadReceipt
	ExpectedFiles int
}

// StartBackupFolderUpload accepts only the registry's validated member list.
// Use the same remote folder resolution and streaming uploader as manual uploads.
func (s *Service) StartBackupFolderUpload(parent, name string, prepare func(context.Context) (string, map[string]string, error), finish func(BackupFolderReceipt, error) error) error {
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
	s.startUploadTaskLocked(c, name, 0, "preparing", func(ctx context.Context, account string) (remoteFile, error) {
		receipt := BackupFolderReceipt{AccountID: account}
		root, expected, err := prepare(s.uploadPreparation(ctx))
		receipt.ExpectedFiles = len(expected)
		var result remoteFile
		if err == nil {
			ctx = context.WithValue(ctx, folderUploadRequest{}, true)
			result, err = s.uploadBackupFolder(ctx, c, id, root, expected, &receipt)
		}
		return result, errors.Join(err, finish(receipt, err))
	})
	return nil
}

func (c *Client) matchingChild(ctx context.Context, parent int64, name string) (remoteFile, bool, error) {
	var result remoteFile
	err := c.walkChildren(ctx, parent, 100, func(file remoteFile) error {
		if strings.EqualFold(file.Name, name) {
			if result.ID != 0 && result.ID != file.ID {
				return errors.New("云端同名对象有多个，无法确认目标")
			}
			result = file
		}
		return nil
	})
	return result, result.ID > 0, err
}

func backupFolderDestination(ctx context.Context, c *Client, parent int64, name string, receipt *BackupFolderReceipt) (int64, error) {
	existing, found, err := c.matchingChild(ctx, parent, name)
	if err != nil {
		return 0, err
	}
	if found && existing.Name != name {
		return 0, errors.New("云端同名目录的大小写与备份不一致")
	}
	entry := folderEntry{Directory: true}
	if err := prepareFolderDestination(ctx, c, parent, name, &entry, existing, found); err != nil {
		return 0, err
	}
	if entry.State != "done" {
		return 0, errors.New("云端同名对象阻挡备份文件夹")
	}
	receipt.Objects = append(receipt.Objects, UploadReceipt{
		FileID: strconv.FormatInt(entry.RemoteID, 10), ParentID: strconv.FormatInt(parent, 10), Name: name, Directory: true,
	})
	return entry.RemoteID, nil
}

func (s *Service) uploadBackupFolder(ctx context.Context, c *Client, parent int64, root string, expected map[string]string, receipt *BackupFolderReceipt) (remoteFile, error) {
	s.resetUploadPart("checking", 0, 0, 0)
	root, _, err := s.selectedFolder(root)
	if err != nil {
		return remoteFile{}, err
	}
	if err := c.folder(ctx, parent); err != nil {
		return remoteFile{}, err
	}
	name := filepath.Base(root)
	id, err := backupFolderDestination(ctx, c, parent, name, receipt)
	if err != nil {
		return remoteFile{}, err
	}
	receipt.FolderID = strconv.FormatInt(id, 10)
	meta, err := backupFolderDestination(ctx, c, id, ".qman", receipt)
	if err != nil {
		return remoteFile{}, err
	}
	existing := make(map[int64]map[string]remoteFile, 2)
	allowed := map[int64]map[string]bool{id: {foldedName(".qman"): true}, meta: {}}
	for path := range expected {
		dir := id
		if path == ".qman/files.jsonl" {
			dir = meta
		}
		allowed[dir][foldedName(filepath.Base(path))] = true
	}
	for _, directory := range []int64{id, meta} {
		files := map[string]remoteFile{}
		err := c.walkChildren(ctx, directory, 100, func(file remoteFile) error {
			key := foldedName(file.Name)
			if !allowed[directory][key] {
				return errors.New("云端备份文件夹包含其他文件，请选择另一个上传位置")
			}
			if _, ok := files[key]; ok {
				file.ID = 0
			}
			files[key] = file
			return nil
		})
		if err != nil {
			return remoteFile{}, err
		}
		existing[directory] = files
	}
	paths := make([]string, 0, len(expected))
	for path := range expected {
		if path != "backup.json" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	paths = append(paths, "backup.json")
	var total int64
	for i, path := range paths {
		directory := id
		if path == ".qman/files.jsonl" {
			directory = meta
		} else if path != filepath.Base(path) || !validName(path) {
			return remoteFile{}, errors.New("备份上传清单路径无效")
		}
		name := filepath.Base(path)
		old, found := existing[directory][foldedName(name)]
		file, err := s.uploadBackupMember(ctx, c, root, path, expected[path], directory, old, found, i+1, len(paths))
		if err != nil {
			return remoteFile{}, fmt.Errorf("%s：%w", name, err)
		}
		total += file.Size
		receipt.Objects = append(receipt.Objects, UploadReceipt{FileID: strconv.FormatInt(file.ID, 10), ParentID: strconv.FormatInt(directory, 10), Name: file.Name, MD5: file.MD5, Size: file.Size})
	}
	return remoteFile{ID: id, Name: name, Size: total}, nil
}

func (s *Service) resetUploadPart(phase string, part, parts int, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := &s.status.Upload
	status.Phase, status.Done, status.Total = phase, 0, total
	status.Part, status.Parts = part, parts
}

func (s *Service) uploadBackupMember(ctx context.Context, c *Client, root, path, expected string, parent int64, old remoteFile, found bool, part, parts int) (remoteFile, error) {
	f, err := media.OpenRelativeWithoutWriters(root, filepath.FromSlash(path))
	if err != nil {
		return remoteFile{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || len(expected) != 64 {
		return remoteFile{}, errors.New("备份文件或清单无法核实")
	}
	name := filepath.Base(path)
	current := filepath.Join(root, filepath.FromSlash(path))
	unchanged := func() bool {
		return sameFile(f, current, info)
	}
	s.resetUploadPart("hashing", part, parts, info.Size())
	sha, md := sha256.New(), md5.New()
	reader := &progressReader{r: io.LimitReader(f, info.Size()+1), ctx: ctx, update: func(n int64) { s.progress("hashing", n) }}
	n, err := io.CopyBuffer(io.MultiWriter(sha, md), reader, make([]byte, 256<<10))
	if err != nil {
		return remoteFile{}, err
	}
	if n != info.Size() || hex.EncodeToString(sha.Sum(nil)) != expected || !unchanged() {
		return remoteFile{}, errors.New("备份文件内容已变化")
	}
	digest := hex.EncodeToString(md.Sum(nil))
	if !found {
		return c.sendFile(ctx, parent, f, info, digest, unchanged, s.progress)
	}
	s.progress("verifying", info.Size())
	if old.ID <= 0 || old.Type != 0 {
		return remoteFile{}, errors.New("云端有冲突的同名对象")
	}
	file, err := c.info(ctx, old.ID)
	if err != nil {
		return remoteFile{}, err
	}
	if file.Trashed || file.Type != 0 || file.Parent != parent || file.Name != name || file.Size != info.Size() || !strings.EqualFold(file.MD5, digest) {
		return remoteFile{}, errors.New("云端同名文件与本地内容不同")
	}
	return file, nil
}

func (s *Service) CheckBackupFolder(ctx context.Context, account string, objects []UploadReceipt, expectedFiles int) (string, string) {
	if len(objects) == 0 || expectedFiles < 3 {
		return "unknown", "云端分包记录不完整"
	}
	ctx = context.WithValue(ctx, folderUploadRequest{}, true)
	present, missing, changed := 0, 0, 0
	members := 0
	for _, object := range objects {
		object.AccountID = account
		state, detail := s.CheckBackupObject(ctx, object)
		switch state {
		case "present":
			present++
			if !object.Directory {
				members++
			}
		case "missing":
			missing++
		case "changed":
			changed++
		default:
			return state, detail
		}
	}
	switch {
	case present == len(objects) && members == expectedFiles:
		return "present", "清单和全部分包已在云端核对"
	case missing == len(objects):
		return "missing", "云端备份目录及成员已缺失"
	default:
		if missing+changed > 0 {
			return "changed", fmt.Sprintf("已核对 %d / %d 个备份文件，有成员缺失或变化", members, expectedFiles)
		}
		return "changed", fmt.Sprintf("已核对 %d / %d 个备份文件，上传未完成", members, expectedFiles)
	}
}
