package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func TestNativeRecycleConfirmationAndAbort(t *testing.T) {
	if os.Getenv("QMAN_NATIVE_RECYCLE_TEST") != "1" {
		t.Skip("opt-in: synthetic Recycle Bin test")
	}
	path := filepath.Join(t.TempDir(), "Qman-synthetic-recycle-check.txt")
	content := []byte("Qman disposable synthetic recycle fixture")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := recycleNative(context.Background(), path, func() error { calls++; return errors.New("synthetic pre-delete refusal") })
	if err == nil || calls != 1 {
		t.Fatal("pre-delete check was not enforced", calls, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(content) {
		t.Fatal("refused item changed")
	}
	location, err := recycleNative(context.Background(), path, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if location == "" {
		t.Fatal("missing actual Recycle Bin item")
	}
	got, err := os.ReadFile(location)
	if err != nil || string(got) != string(content) {
		t.Fatal("recycle payload not verified", err)
	}
	// Intentionally never empty or permanently delete any Recycle Bin content.
}

//go:noinline
func growRecycleTestStack(depth int) byte {
	var pad [2048]byte
	pad[depth%len(pad)] = byte(depth)
	if depth > 0 {
		pad[0] = growRecycleTestStack(depth - 1)
	}
	runtime.KeepAlive(&pad)
	return pad[0]
}

func TestNativeSinkLifetimeAndQueryInterface(t *testing.T) {
	sinkRegistry.RLock()
	before := len(sinkRegistry.states)
	sinkRegistry.RUnlock()
	state := &recycleState{ctx: context.Background(), check: func() error { return nil }}
	native, err := newRecycleSink(state)
	if err != nil {
		t.Fatal(err)
	}
	address := uintptr(unsafe.Pointer(native))
	runtime.GC()
	growRecycleTestStack(128)
	if uintptr(unsafe.Pointer(native)) != address || native.state() != state {
		t.Fatal("callback state moved or lost")
	}
	var out uintptr
	hr, _, _ := syscall.SyscallN(recycleVtable[0], address, uintptr(unsafe.Pointer(&iidProgressSink)), uintptr(unsafe.Pointer(&out)))
	if hr != 0 || out != address || native.refs != 2 {
		t.Fatal("QueryInterface must own a reference")
	}
	if native.release() != 1 {
		t.Fatal("COM reference not released")
	}
	if native.release() != 0 {
		t.Fatal("owner reference not released")
	}
	sinkRegistry.RLock()
	after := len(sinkRegistry.states)
	sinkRegistry.RUnlock()
	if before != after {
		t.Fatal("callback state leaked")
	}
}

func TestNativeRecycleBatchWithStackGrowth(t *testing.T) {
	if os.Getenv("QMAN_NATIVE_RECYCLE_TEST") != "1" {
		t.Skip("opt-in: synthetic Recycle Bin batch")
	}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := filepath.Join(root, "Qman-synthetic-canceled.txt")
	if err := os.WriteFile(canceled, []byte("synthetic cancellation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := recycleNative(ctx, canceled, func() error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not preserved", err)
	}
	if _, err := os.Stat(canceled); err != nil {
		t.Fatal("canceled file changed")
	}
	for i := 0; i < 16; i++ {
		path := filepath.Join(root, fmt.Sprintf("Qman-synthetic-batch-%02d.txt", i))
		content := []byte(fmt.Sprintf("Qman disposable synthetic item %d", i))
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		location, err := recycleNative(context.Background(), path, func() error { growRecycleTestStack(128); runtime.GC(); return nil })
		if err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
		got, err := os.ReadFile(location)
		if err != nil || string(got) != string(content) {
			t.Fatal("recycle item not confirmed", i, err)
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("source still exists", i, err)
		}
	}
}
