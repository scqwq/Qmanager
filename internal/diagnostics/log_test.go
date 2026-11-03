package diagnostics

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationAndRecentEntries(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for i := 0; i < 305; i++ {
		l.Record(WithItem(context.Background(), "task-opaque", "item-opaque"), "info", "test.record", "合成记录")
	}
	snapshot, err := l.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 300 || snapshot.Entries[0].Task != "task-opaque" {
		t.Fatal("recent entries not bounded or context lost")
	}
	// Fill the current file to its boundary without a large test loop.
	if _, err = l.file.Write(make([]byte, maxLogBytes-int(l.size))); err != nil {
		t.Fatal(err)
	}
	l.size = maxLogBytes
	l.Record(context.Background(), "warning", "test.rotated", "轮换后的记录")
	if _, err = os.Stat(l.path(1)); err != nil {
		t.Fatal("rotation missing", err)
	}
	b, err := os.ReadFile(l.path(0))
	if err != nil {
		t.Fatal(err)
	}
	var newest Entry
	if json.Unmarshal(b, &newest) != nil || newest.Event != "test.rotated" {
		t.Fatal("new record not published")
	}
	if info, _ := os.Stat(l.path(0)); info.Size() > maxLogBytes {
		t.Fatal("unbounded log")
	}
}

func TestCrashOutputInChildProcess(t *testing.T) {
	if dir := os.Getenv("QMAN_TEST_CRASH_DIR"); dir != "" {
		l, err := Open(dir)
		if err != nil {
			panic(err)
		}
		if err = l.EnableCrashOutput(); err != nil {
			panic(err)
		}
		panic("Qman synthetic diagnostics panic")
	}
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOutputInChildProcess$")
		cmd.Env = append(os.Environ(), "QMAN_TEST_CRASH_DIR="+dir)
		b, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(b), "Qman synthetic diagnostics panic") {
			t.Fatalf("synthetic panic was not observed: %v", err)
		}
	}
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	paths, err := l.crashFiles()
	if err != nil || len(paths) != 3 {
		t.Fatal("crash retention", len(paths), err)
	}
	snapshot, err := l.Snapshot()
	if err != nil || len(snapshot.CrashText) != 3 {
		t.Fatal("missing crash details", err)
	}
	if !strings.Contains(snapshot.CrashText[0].Text, "Qman synthetic diagnostics panic") {
		t.Fatal("panic not written to local file")
	}
}

func TestCrashPreviewIsBounded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "crash-20261102.log"), []byte(strings.Repeat("x", maxReadBytes+10)), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s, err := l.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.CrashText) != 1 || !s.CrashText[0].Truncated || len(s.CrashText[0].Text) != maxReadBytes {
		t.Fatal("unbounded crash preview")
	}
}
