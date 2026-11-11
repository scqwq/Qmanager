package cloud123

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"qqarchive/internal/media"
)

type folderDestination struct {
	id    int64
	state string
}

func foldedName(name string) string {
	return strings.Map(func(r rune) rune {
		first := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			first = min(first, next)
		}
		return first
	}, name)
}

func (e folderEntry) destination(rootName string) (parent, name string) {
	if e.Path == "." {
		return "", rootName
	}
	return filepath.Dir(e.Path), filepath.Base(e.Path)
}

func (s *Service) uploadFolderEntries(ctx context.Context, c *Client, db *sql.DB, account string, job *folderJob, status *FolderUploadStatus) error {
	root, identity, err := s.selectedFolder(job.Root)
	if err != nil || identity != job.RootIdentity {
		return errors.New("原文件夹已移动、离线或更换，请检查后继续")
	}
	if err := c.folder(ctx, job.Parent); err != nil {
		return err
	}
	directories := map[string]folderDestination{"": {id: job.Parent}}
	plannedNames := make(map[string]map[string]bool)
	for _, e := range job.Entries {
		parent, name := e.destination(job.Name)
		if plannedNames[parent] == nil {
			plannedNames[parent] = make(map[string]bool)
		}
		plannedNames[parent][foldedName(name)] = true
	}
	children := make(map[int64]map[string]remoteFile)
	for seq, entry := range job.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		parentPath, name := entry.destination(job.Name)
		parent, known := directories[parentPath]
		if !known {
			return errors.New("无法确认清单中父目录的映射")
		}
		if entry.Batch > status.Batch {
			status.Batch = entry.Batch
			clear(children)
		}
		if entry.State == "failed" || entry.State == "skipped" {
			if entry.Directory {
				directories[entry.Path] = folderDestination{state: entry.State}
			}
			continue
		}
		if !entry.Directory && entry.State == "done" {
			continue
		}
		status.Current, status.Step = entry.Path, "checking"
		s.setFolderStatus(account, *status)
		if parent.state != "" {
			entry.State, entry.Detail = parent.state, "上级目录无法使用"
		} else {
			if !validName(name) || !filepath.IsLocal(entry.Path) {
				return errors.New("上传清单路径无法核实")
			}
			list, ok := children[parent.id]
			if !ok {
				list = make(map[string]remoteFile)
				err := c.walkChildren(ctx, parent.id, 1000, func(file remoteFile) error {
					key := foldedName(file.Name)
					if plannedNames[parentPath][key] {
						if previous, exists := list[key]; exists && previous.ID != file.ID {
							file.ID = 0
						}
						list[key] = file
					}
					return nil
				})
				if err != nil {
					return err
				}
				children[parent.id] = list
			}
			key := foldedName(name)
			existing, found := list[key]
			if entry.Directory {
				err = prepareFolderDestination(ctx, c, parent.id, name, &entry, existing, found)
			} else {
				err = s.uploadFolderFile(ctx, c, db, seq, account, root, parent.id, name, &entry, existing, found)
			}
			if err != nil {
				return err
			}
			if entry.State == "done" && !found {
				kind := 0
				if entry.Directory {
					kind = 1
				}
				list[key] = remoteFile{ID: entry.RemoteID, Name: name, Type: kind, Parent: parent.id, Size: entry.Size, MD5: entry.Digest}
			}
		}
		if err := saveFolderEntry(db, seq, entry); err != nil {
			return err
		}
		if entry.Directory {
			if entry.State == "done" {
				directories[entry.Path] = folderDestination{id: entry.RemoteID}
			} else {
				directories[entry.Path] = folderDestination{state: entry.State}
			}
		}
		if job.Entries[seq].State != entry.State {
			status.countResult(entry)
			s.setFolderStatus(account, *status)
		}
	}
	status.Current, status.Step = "", ""
	return nil
}

func prepareFolderDestination(ctx context.Context, c *Client, parent int64, name string, entry *folderEntry, existing remoteFile, found bool) error {
	if entry.State == "done" && (!found || existing.ID != entry.RemoteID) {
		return errors.New("已登记的云端目录发生变化，请核对原位置后继续")
	}
	if found {
		if existing.ID <= 0 || existing.Type != 1 {
			entry.State, entry.Detail = "skipped", "云端同名对象阻挡目录"
			return nil
		}
	} else {
		folder, err := c.createFolder(ctx, parent, name)
		if err != nil {
			return err
		}
		existing.ID, err = parseID(folder.ID)
		if err != nil {
			return err
		}
	}
	file, err := c.info(ctx, existing.ID)
	if err != nil {
		return err
	}
	if file.Trashed || file.Type != 1 || file.Parent != parent || !strings.EqualFold(file.Name, name) {
		return errors.New("云端文件夹的名称或位置无法确认")
	}
	entry.State, entry.RemoteID = "done", file.ID
	return nil
}

func (s *Service) uploadFolderFile(ctx context.Context, c *Client, db *sql.DB, seq int, account, root string, parent int64, name string, entry *folderEntry, existing remoteFile, found bool) error {
	if found {
		if entry.State == "sending" && existing.Type == 0 && existing.ID > 0 {
			file, err := c.info(ctx, existing.ID)
			if err != nil {
				return err
			}
			if file.Trashed || file.Type != 0 || file.Parent != parent || !strings.EqualFold(file.Name, name) {
				return errors.New("中断文件的云端位置发生变化，请重新核对")
			}
			if file.Size == entry.Size && file.MD5 == "" {
				return errors.New("云端文件摘要尚未返回，请稍后继续核对")
			}
			if file.Size == entry.Size && file.Name == name && strings.EqualFold(file.MD5, entry.Digest) {
				entry.State, entry.RemoteID = "done", file.ID
				return nil
			}
		}
		entry.State, entry.Detail = "skipped", "云端已有同名对象"
		return nil
	}

	f, err := media.OpenRelative(root, entry.Path)
	if err != nil {
		entry.State, entry.Detail = "failed", "本地文件已移动或无法读取"
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size || info.ModTime().UnixNano() != entry.Modified || media.FileIdentity(f) != entry.Identity {
		entry.State, entry.Detail = "failed", "本地文件已变化"
		return nil
	}
	unchanged := func() bool {
		current, err := media.OpenRelative(root, entry.Path)
		if err != nil {
			return false
		}
		defer current.Close()
		now, err := current.Stat()
		return err == nil && os.SameFile(now, info) && now.Size() == entry.Size && now.ModTime().UnixNano() == entry.Modified
	}
	digest, err := uploadDigest(ctx, f, func(int64) {})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry.State, entry.Detail = "failed", "无法完整读取本地文件"
		return nil
	}
	if !unchanged() || (entry.State == "sending" && entry.Digest != digest) {
		entry.State, entry.Detail = "failed", "本地文件内容已变化"
		return nil
	}
	entry.State, entry.Digest = "sending", digest
	if err := saveFolderEntry(db, seq, *entry); err != nil {
		return err
	}
	path := entry.Path
	file, err := c.sendFile(ctx, parent, f, info, digest, unchanged, func(step string, _ int64) {
		s.mu.Lock()
		if s.folderAccount == account && s.folderStatus.Phase == "running" && s.folderStatus.Current == path {
			s.folderStatus.Step = step
		}
		s.mu.Unlock()
	})
	if err != nil {
		return err
	}
	entry.State, entry.RemoteID = "done", file.ID
	return nil
}
