package cloud123

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
	"qqarchive/internal/media"
)

type FolderUploadStatus struct {
	FileDone  int64  `json:"file_done,omitempty"`
	FileTotal int64  `json:"file_total,omitempty"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Detail    string `json:"detail"`
	Current   string `json:"current"`
	Step      string `json:"step"`
	Total     int    `json:"total"`
	Uploaded  int    `json:"uploaded"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
	Batch     int    `json:"batch"`
	Batches   int    `json:"batches"`
	Issues    int    `json:"issues"`
}

type FolderUploadIssue struct {
	Path   string `json:"path"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type folderJob struct {
	ID, Root, RootIdentity, Name, Phase, Detail string
	Parent                                      int64
	Entries                                     []folderEntry
}

type folderEntry struct {
	Path, Identity, State, Digest, Detail string
	Directory                             bool
	Size, Modified, RemoteID              int64
	Batch                                 int
}

func (s *Service) openFolderStore(account string, create bool) (*sql.DB, error) {
	path := filepath.Join(filepath.Dir(s.credential), "folder-upload-"+account+".sqlite")
	if _, err := media.Canonical(path); err != nil {
		return nil, errors.New("文件夹上传记录路径不安全")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("文件夹上传记录路径不安全")
		}
	} else if !os.IsNotExist(err) || !create {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA trusted_schema=OFF;
CREATE TABLE IF NOT EXISTS folder_job (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  id TEXT NOT NULL, root TEXT NOT NULL, identity TEXT NOT NULL, name TEXT NOT NULL,
  parent INTEGER NOT NULL, phase TEXT NOT NULL, detail TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS folder_entries (
  seq INTEGER PRIMARY KEY, path TEXT NOT NULL, directory INTEGER NOT NULL, batch INTEGER NOT NULL,
  size INTEGER NOT NULL, modified INTEGER NOT NULL, identity TEXT NOT NULL,
  state TEXT NOT NULL, digest TEXT NOT NULL, remote_id INTEGER NOT NULL, detail TEXT NOT NULL
);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func saveFolderJob(db *sql.DB, job folderJob) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM folder_entries; DELETE FROM folder_job"); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO folder_job VALUES(1,?,?,?,?,?,?,?)", job.ID, job.Root, job.RootIdentity, job.Name, job.Parent, job.Phase, job.Detail); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO folder_entries VALUES(?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for seq, e := range job.Entries {
		if _, err = stmt.Exec(seq, e.Path, e.Directory, e.Batch, e.Size, e.Modified, e.Identity, e.State, e.Digest, e.RemoteID, e.Detail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readFolderJob(db *sql.DB) (folderJob, error) {
	var job folderJob
	err := db.QueryRow("SELECT id,root,identity,name,parent,phase,detail FROM folder_job WHERE singleton=1").
		Scan(&job.ID, &job.Root, &job.RootIdentity, &job.Name, &job.Parent, &job.Phase, &job.Detail)
	if err != nil {
		return job, err
	}
	rows, err := db.Query("SELECT path,directory,batch,size,modified,identity,state,digest,remote_id,detail FROM folder_entries ORDER BY seq")
	if err != nil {
		return job, err
	}
	defer rows.Close()
	for rows.Next() {
		var e folderEntry
		if err := rows.Scan(&e.Path, &e.Directory, &e.Batch, &e.Size, &e.Modified, &e.Identity, &e.State, &e.Digest, &e.RemoteID, &e.Detail); err != nil {
			return job, err
		}
		job.Entries = append(job.Entries, e)
		if len(job.Entries) > folderEntryLimit {
			return job, errors.New("文件夹上传清单超过试用版上限")
		}
	}
	return job, rows.Err()
}

func saveFolderEntry(db *sql.DB, seq int, e folderEntry) error {
	_, err := db.Exec("UPDATE folder_entries SET state=?,digest=?,remote_id=?,detail=? WHERE seq=?", e.State, e.Digest, e.RemoteID, e.Detail, seq)
	if err != nil {
		return errors.New("无法保存文件上传结果，已暂停")
	}
	return nil
}

func folderSummary(job folderJob) FolderUploadStatus {
	status := FolderUploadStatus{ID: job.ID, Name: job.Name, Phase: job.Phase, Detail: job.Detail}
	for _, e := range job.Entries {
		if !e.Directory {
			status.Total++
			status.Batches = max(status.Batches, e.Batch)
		}
		status.countResult(e)
		if e.State == "sending" {
			status.Current = e.Path
		}
	}
	if status.Phase == "running" {
		status.Phase = "paused"
		status.Detail = "上传已中断，可以继续核对并上传剩余文件"
	}
	return status
}

func (s *FolderUploadStatus) countResult(e folderEntry) {
	switch e.State {
	case "done":
		if !e.Directory {
			s.Uploaded++
		}
	case "skipped", "failed":
		s.Issues++
		if !e.Directory {
			if e.State == "skipped" {
				s.Skipped++
			} else {
				s.Failed++
			}
		}
	}
}
