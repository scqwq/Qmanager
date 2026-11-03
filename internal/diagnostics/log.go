// Package diagnostics records bounded, local application diagnostics. Callers
// supply fixed messages and opaque IDs, never media paths or arbitrary errors.
package diagnostics

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxLogBytes = 1 << 20
const maxReadBytes = 256 << 10
const logFiles = 3

type Entry struct {
	At     time.Time `json:"at"`
	Level  string    `json:"level"`
	Event  string    `json:"event"`
	Task   string    `json:"task,omitempty"`
	Item   string    `json:"item,omitempty"`
	Detail string    `json:"detail"`
}
type Crash struct {
	At     time.Time `json:"at"`
	App    string    `json:"app"`
	Module string    `json:"module"`
	Code   string    `json:"code"`
	Offset string    `json:"offset"`
}
type CrashText struct {
	At        time.Time `json:"at"`
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"`
}
type Snapshot struct {
	Entries    []Entry     `json:"entries"`
	Crashes    []Crash     `json:"crashes"`
	CrashText  []CrashText `json:"crash_text"`
	SystemNote string      `json:"system_note"`
	WriteNote  string      `json:"write_note"`
	Directory  string      `json:"directory"`
}
type Logger struct {
	mu           sync.Mutex
	dir          string
	file         *os.File
	size         int64
	writeNote    string
	crashPath    string
	crashEnabled bool
}

var active atomic.Pointer[Logger]

type traceKey struct{}
type trace struct{ Task, Item string }

func WithItem(ctx context.Context, task, item string) context.Context {
	return context.WithValue(ctx, traceKey{}, trace{Task: task, Item: item})
}
func SetDefault(l *Logger) { active.Store(l) }
func Default() *Logger     { return active.Load() }
func Record(ctx context.Context, level, event, detail string) {
	if l := active.Load(); l != nil {
		l.Record(ctx, level, event, detail)
	}
}

func Open(dir string) (*Logger, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("日志目录不可用")
	}
	l := &Logger{dir: dir}
	for i := 0; i < logFiles; i++ {
		if info, err := os.Lstat(l.path(i)); err == nil && !info.Mode().IsRegular() {
			return nil, errors.New("日志文件类型异常")
		}
	}
	l.file, err = os.OpenFile(l.path(0), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.New("无法打开本地日志")
	}
	info, err = l.file.Stat()
	if err != nil {
		l.file.Close()
		return nil, err
	}
	l.size = info.Size()
	return l, nil
}
func (l *Logger) path(i int) string { return filepath.Join(l.dir, fmt.Sprintf("events-%d.jsonl", i)) }
func (l *Logger) rotate() error {
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	if err := os.Remove(l.path(logFiles - 1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := logFiles - 2; i >= 0; i-- {
		if err := os.Rename(l.path(i), l.path(i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	f, err := os.OpenFile(l.path(0), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	l.file = f
	l.size = 0
	return nil
}
func clipped(s string, n int) string {
	if len(s) > n {
		return strings.ToValidUTF8(s[:n], "")
	}
	return s
}
func (l *Logger) Record(ctx context.Context, level, event, detail string) {
	tr, _ := ctx.Value(traceKey{}).(trace)
	e := Entry{At: time.Now().UTC(), Level: clipped(level, 16), Event: clipped(event, 80), Task: clipped(tr.Task, 100), Item: clipped(tr.Item, 100), Detail: clipped(detail, 1000)}
	b, _ := json.Marshal(e)
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	if l.size+int64(len(b)) > maxLogBytes {
		if err := l.rotate(); err != nil {
			l.writeNote = "日志轮换失败，后续记录可能不完整。"
			return
		}
	}
	n, err := l.file.Write(b)
	l.size += int64(n)
	// Writes go straight to the OS, not a user-space buffer. Sync only exceptional
	// records; forcing disk flushes for every callback would slow large cleanups.
	if err == nil && (level == "error" || level == "warning") {
		err = l.file.Sync()
	}
	if err != nil {
		l.writeNote = "日志写入失败，记录可能不完整。"
	}
}

// SetCrashOutput duplicates the file handle. A crash report is kept separate
// from JSON events and never truncated while the runtime may be writing it.
func (l *Logger) EnableCrashOutput() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	files, err := l.crashFiles()
	if err != nil {
		return err
	}
	for i, path := range files {
		info, e := os.Lstat(path)
		if e != nil {
			continue
		}
		if info.Size() == 0 || i >= 2 {
			if e = os.Remove(path); e != nil {
				return e
			}
		}
	}
	var id [8]byte
	if _, err = rand.Read(id[:]); err != nil {
		return err
	}
	path := filepath.Join(l.dir, "crash-"+time.Now().UTC().Format("20060102T150405.000000000")+"-"+hex.EncodeToString(id[:])+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = debug.SetCrashOutput(f, debug.CrashOptions{})
	closeErr := f.Close()
	if err != nil {
		os.Remove(path)
		return err
	}
	l.crashPath, l.crashEnabled = path, true
	return closeErr
}
func (l *Logger) crashFiles() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "crash-") && strings.HasSuffix(e.Name(), ".log") && e.Type().IsRegular() {
			paths = append(paths, filepath.Join(l.dir, e.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}
func (l *Logger) Snapshot() (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := Snapshot{Entries: []Entry{}, Crashes: []Crash{}, CrashText: []CrashText{}, Directory: l.dir, WriteNote: l.writeNote}
	for i := logFiles - 1; i >= 0; i-- {
		f, err := os.Open(l.path(i))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return s, errors.New("无法读取本地日志")
		}
		reader := bufio.NewScanner(io.LimitReader(f, maxLogBytes))
		for reader.Scan() {
			var e Entry
			if json.Unmarshal(reader.Bytes(), &e) == nil {
				s.Entries = append(s.Entries, e)
			}
			if len(s.Entries) > 300 {
				s.Entries = s.Entries[len(s.Entries)-300:]
			}
		}
		err = reader.Err()
		f.Close()
		if err != nil {
			return s, errors.New("部分日志无法读取")
		}
	}
	for i, j := 0, len(s.Entries)-1; i < j; i, j = i+1, j-1 {
		s.Entries[i], s.Entries[j] = s.Entries[j], s.Entries[i]
	}
	paths, err := l.crashFiles()
	if err != nil {
		return s, errors.New("无法读取崩溃记录")
	}
	for _, path := range paths {
		if len(s.CrashText) >= 3 {
			break
		}
		f, e := os.Open(path)
		if e != nil {
			continue
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			continue
		}
		b, e := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
		f.Close()
		if e == nil && len(b) > 0 {
			s.CrashText = append(s.CrashText, CrashText{At: info.ModTime().UTC(), Text: strings.ToValidUTF8(string(b[:min(len(b), maxReadBytes)]), "�"), Truncated: len(b) > maxReadBytes})
		}
	}
	return s, nil
}
func Read() (Snapshot, error) {
	l := active.Load()
	if l == nil {
		return Snapshot{}, errors.New("本地日志未能启动，请检查应用数据目录是否可写")
	}
	s, err := l.Snapshot()
	if err != nil {
		return s, err
	}
	s.Crashes, s.SystemNote = systemCrashes()
	return s, nil
}
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.crashEnabled {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		l.crashEnabled = false
	}
	if l.crashPath != "" {
		if info, err := os.Stat(l.crashPath); err == nil && info.Size() == 0 {
			_ = os.Remove(l.crashPath)
		}
	}
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
