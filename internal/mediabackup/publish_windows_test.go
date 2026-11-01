package mediabackup

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPublishTreeRetriesSharingAndHonorsCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "cancel"}[cancelled], func(t *testing.T) {
			parent := t.TempDir()
			stage := filepath.Join(parent, "stage")
			target := filepath.Join(parent, "published")
			os.Mkdir(stage, 0700)
			p, _ := windows.UTF16PtrFromString(stage)
			handle, err := windows.CreateFile(p, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- publishTree(ctx, stage, target) }()
			select {
			case err = <-done:
				windows.CloseHandle(handle)
				t.Fatal("publish did not wait for shared handle", err)
			case <-time.After(150 * time.Millisecond):
			}
			if cancelled {
				cancel()
			}
			windows.CloseHandle(handle)
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("publish did not finish")
			}
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if _, e := os.Stat(stage); e != nil {
					t.Fatal("canceled publication removed stage")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, e := os.Stat(target); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestPublishTreeNeverOverwritesExistingTarget(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	target := filepath.Join(parent, "published")
	os.Mkdir(stage, 0700)
	os.Mkdir(target, 0700)
	keep := filepath.Join(target, "keep.txt")
	os.WriteFile(keep, []byte("keep"), 0600)
	if err := publishTree(context.Background(), stage, target); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(keep)
	if string(b) != "keep" {
		t.Fatal("overwrote existing destination")
	}
}
