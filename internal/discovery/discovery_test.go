package discovery

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"reflect"
	"strings"
	"testing"
)

func TestOuterDirectoryFindsAccountsWithoutReadingMedia(t *testing.T) {
	root := filepath.Join(t.TempDir(), "迁移目录", "Tencent Files")
	for _, path := range []string{"10001/nt_qq/nt_data/Pic/2026-09/Ori/一.jpg", "10002/nt_qq/nt_data/Video/2026-10/Thumb/二.png", "msf/ignored/Pic/2026-10/Ori/not-an-account.png"} {
		if err := testfixture.Image(root, path); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := testfixture.Snapshot(root)
	r, err := Find(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || len(r.Candidates) != 2 || r.Candidates[0].Name != "10001" || r.Candidates[1].Name != "10002" {
		t.Fatalf("unexpected discovery %+v", r)
	}
	after, _ := testfixture.Snapshot(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source changed")
	}
	for _, c := range r.Candidates {
		if !strings.HasSuffix(c.Root, "nt_data") {
			t.Fatal("wrong candidate root")
		}
	}
}
func TestDirectRootLimitsAndCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	os.MkdirAll(root, 0700)
	r, err := Find(context.Background(), root)
	if err != nil || len(r.Candidates) != 1 {
		t.Fatal("direct root", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Find(ctx, t.TempDir()); err != context.Canceled {
		t.Fatal("discovery cancel", err)
	}
	parent := t.TempDir()
	deep := strings.Repeat("level/", MaxDepth+2) + "nt_data/Pic/2026-10/Ori/test.png"
	if err = testfixture.Image(parent, deep); err != nil {
		t.Fatal(err)
	}
	r, err = Find(context.Background(), parent)
	if err == nil || r.Complete {
		t.Fatal("depth bound not reported")
	}
}
