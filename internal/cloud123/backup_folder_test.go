package cloud123

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRegisteredBackupFolderReusesOnlyMatchingMembers(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "split-backup")
	if err := os.MkdirAll(filepath.Join(root, ".qman"), 0700); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{}
	for _, name := range []string{"backup.json", ".qman/files.jsonl", "part-001.zip", "part-002.zip"} {
		content := []byte(name)
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), content, 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		expected[name] = hex.EncodeToString(sum[:])
	}
	const existing = "part-001.zip"
	digest := md5.Sum([]byte(existing))
	files := map[int64]remoteFile{
		1: {ID: 1, Name: filepath.Base(root), Type: 1},
		2: {ID: 2, Parent: 1, Name: ".qman", Type: 1},
		3: {ID: 3, Parent: 1, Name: existing, Size: int64(len(existing)), MD5: hex.EncodeToString(digest[:])},
	}
	var uploaded []string
	transport := func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/b/api/file/list/new":
			parent, _ := strconv.ParseInt(req.URL.Query().Get("parentFileId"), 10, 64)
			list := []remoteFile{}
			for _, file := range files {
				if file.Parent == parent {
					list = append(list, file)
				}
			}
			return ok(map[string]any{"Next": "-1", "InfoList": list}), nil
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
		case "/b/api/file/upload_request":
			var body struct {
				Name   string `json:"fileName"`
				Digest string `json:"etag"`
				Parent int64  `json:"parentFileId"`
				Size   int64  `json:"size"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				return nil, err
			}
			uploaded = append(uploaded, body.Name)
			file := remoteFile{ID: int64(len(files) + 10), Parent: body.Parent, Name: body.Name, Size: body.Size, MD5: body.Digest}
			files[file.ID] = file
			return ok(map[string]any{"Reuse": true, "Info": file}), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL.Path)
		}
	}
	s := testService(t, transport)
	s.client.batchInterval = 0
	var receipt BackupFolderReceipt
	var failed error
	start := func() {
		t.Helper()
		err := s.StartBackupFolderUpload("0", "split", func(context.Context) (string, map[string]string, error) {
			return root, expected, nil
		}, func(got BackupFolderReceipt, e error) error {
			receipt, failed = got, e
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		waitFolderJob(t, s)
	}
	start()
	if failed != nil || receipt.FolderID != "1" || len(receipt.Objects) != 6 || len(uploaded) != 3 || uploaded[2] != "backup.json" {
		t.Fatalf("invalid group result: %+v, uploaded %v, %v", receipt, uploaded, failed)
	}
	if state, detail := s.CheckBackupFolder(ctx, receipt.AccountID, receipt.Objects, receipt.ExpectedFiles); state != "present" {
		t.Fatalf("cloud backup should be complete: %s, %s", state, detail)
	}
	bad := files[3]
	bad.MD5 = "different"
	files[3] = bad
	if state, _ := s.CheckBackupFolder(ctx, receipt.AccountID, receipt.Objects, receipt.ExpectedFiles); state != "changed" {
		t.Fatalf("changed cloud member reported %s", state)
	}
	start()
	if failed == nil || len(uploaded) != 3 || s.Status().Upload.Phase != "failed" || errors.Is(failed, context.Canceled) {
		t.Fatalf("conflicting same-name object was accepted or overwritten: %v, %v", uploaded, failed)
	}
	if state, _ := s.CheckBackupFolder(ctx, receipt.AccountID, receipt.Objects, receipt.ExpectedFiles); state != "changed" {
		t.Fatalf("partial upload was reported as %s", state)
	}
}
