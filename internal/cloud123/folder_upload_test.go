package cloud123

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestFolderUploadBatchBoundaryAndRecovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "photos")
	if err := os.MkdirAll(filepath.Join(root, "z-empty"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= folderBatchSize; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%04d.bin", i)), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const lostResponse = "file-0949.bin"
	files := map[int64]remoteFile{
		1: {ID: 1, Name: "photos", Type: 1},
		2: {ID: 2, Name: "FILE-0000.bin", Parent: 1, Size: 9, MD5: "original"},
	}
	inits := make(map[string]int)
	var pending remoteFile
	transport := func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/b/api/file/list/new":
			parent, _ := strconv.ParseInt(req.URL.Query().Get("parentFileId"), 10, 64)
			next, _ := strconv.Atoi(req.URL.Query().Get("next"))
			var ids []int64
			for id, file := range files {
				if file.Parent == parent {
					ids = append(ids, id)
				}
			}
			slices.Sort(ids)
			end := min(next+100, len(ids))
			page := make([]remoteFile, 0, end-next)
			for _, id := range ids[next:end] {
				page = append(page, files[id])
			}
			cursor := "-1"
			if end < len(ids) {
				cursor = strconv.Itoa(end)
			}
			return ok(map[string]any{"Next": cursor, "InfoList": page}), nil
		case "/b/api/file/upload_request":
			var body struct {
				Name   string `json:"fileName"`
				Digest string `json:"etag"`
				Parent int64  `json:"parentFileId"`
				Size   int64  `json:"size"`
				Type   int    `json:"type"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			inits[body.Name]++
			file := remoteFile{ID: int64(len(files) + 10), Name: body.Name, Parent: body.Parent, Size: body.Size, MD5: body.Digest, Type: body.Type}
			if body.Type == 1 {
				files[file.ID] = file
				return ok(map[string]any{"Info": file}), nil
			}
			if body.Name == lostResponse {
				pending = file
				return ok(map[string]any{"FileId": 3, "Bucket": "test", "Key": "test-key", "UploadId": "test-upload", "StorageNode": "test", "SliceSize": "262144"}), nil
			}
			files[file.ID] = file
			return ok(map[string]any{"Reuse": true, "Info": file}), nil
		case "/b/api/file/s3_upload_object/auth":
			return ok(map[string]any{"presignedUrls": map[string]string{"1": "https://unit.123pan.com/test-object"}}), nil
		case "/test-object":
			return ok(nil), nil
		case "/b/api/file/upload_complete/v2":
			files[pending.ID] = pending
			return nil, errors.New("mock: completion response lost")
		case "/b/api/file/info":
			var body struct {
				IDs []struct {
					ID int64 `json:"fileId"`
				} `json:"fileIdList"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			return ok(map[string]any{"infoList": []remoteFile{files[body.IDs[0].ID]}}), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	}
	first := testService(t, transport)
	choice, err := first.SelectFolder(context.Background(), root)
	if err != nil || choice.Files != 951 || choice.Batches != 2 {
		t.Fatalf("unexpected plan: %+v, %v", choice, err)
	}
	if err := first.StartFolderUpload("0", choice.Ticket); err != nil {
		t.Fatal(err)
	}
	waitFolderJob(t, first)
	before, err := first.FolderUploadStatus()
	if err != nil || before.Phase != "paused" || before.Batch != 1 || before.Skipped != 1 || before.Uploaded != 948 || inits["file-0950.bin"] != 0 {
		t.Fatalf("second batch ran or first batch was miscounted: %+v, %v", before, err)
	}
	first.Close()

	reopened := testService(t, transport)
	reopened.credential = first.credential
	before, err = reopened.FolderUploadStatus()
	if err != nil || before.Phase != "paused" || before.ID != choice.Ticket {
		t.Fatalf("unable to restore job: %+v, %v", before, err)
	}
	if err := reopened.ResumeFolderUpload(before.ID); err != nil {
		t.Fatal(err)
	}
	waitFolderJob(t, reopened)
	final, err := reopened.FolderUploadStatus()
	if err != nil || final.Phase != "complete" || final.Uploaded != 950 || final.Skipped != 1 || final.Issues != 1 || inits[lostResponse] != 1 || inits["file-0950.bin"] != 1 || inits["file-0000.bin"] != 0 || inits["z-empty"] != 1 {
		t.Fatalf("unexpected completion: %+v, %v", final, err)
	}
	issues, err := reopened.FolderUploadIssues(final.ID, 0)
	if err != nil || len(issues) != 1 || issues[0].Path != "file-0000.bin" || issues[0].State != "skipped" || files[2].MD5 != "original" {
		t.Fatalf("same-name item was changed or unreported: %+v, %v", issues, err)
	}
}

func waitFolderJob(t *testing.T, s *Service) {
	t.Helper()
	s.mu.RLock()
	done := s.done
	s.mu.RUnlock()
	select {
	case <-done:
	case <-time.After(120 * time.Second):
		status, _ := s.FolderUploadStatus()
		t.Fatalf("folder job did not stop: %+v", status)
	}
}
