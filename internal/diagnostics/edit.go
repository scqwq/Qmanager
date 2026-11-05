package diagnostics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func diagnosticID(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func EditRecord(kind, id string) error {
	l := Default()
	if l == nil || id == "" {
		return errors.New("日志或所选记录不可用")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch kind {
	case "run":
		return l.deleteEntry(id)
	case "crash":
		paths, err := l.crashFiles()
		if err != nil {
			return err
		}
		for _, path := range paths {
			if filepath.Base(path) == id && path != l.crashPath {
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					return errors.New("所选崩溃报告已变化")
				}
				return os.Remove(path)
			}
		}
		return errors.New("所选崩溃报告已不存在或仍在写入")
	case "system":
		if len(id) != 64 {
			return errors.New("无效的系统事件编号")
		}
		if _, err := hex.DecodeString(id); err != nil {
			return errors.New("无效的系统事件编号")
		}
		hidden, err := l.hiddenCrashes()
		if err != nil {
			return err
		}
		for _, key := range hidden {
			if key == id {
				return nil
			}
		}
		hidden = append(hidden, id)
		if len(hidden) > 512 {
			hidden = hidden[len(hidden)-512:]
		}
		data, err := json.Marshal(hidden)
		if err != nil {
			return err
		}
		return replaceLog(l.dir, filepath.Join(l.dir, "hidden-crashes.json"), data)
	default:
		return errors.New("无效的日志类别")
	}
}

func (l *Logger) hiddenCrashes() ([]string, error) {
	path := filepath.Join(l.dir, "hidden-crashes.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, errors.New("无法读取隐藏记录")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	err = json.Unmarshal(data, &out)
	return out, err
}

func (l *Logger) visibleSystemCrashes(crashes []Crash) []Crash {
	l.mu.Lock()
	defer l.mu.Unlock()
	hidden, _ := l.hiddenCrashes()
	removed := make(map[string]bool, len(hidden))
	for _, id := range hidden {
		removed[id] = true
	}
	visible := []Crash{}
	for _, crash := range crashes {
		data, _ := json.Marshal(crash)
		crash.ID = diagnosticID(data)
		if !removed[crash.ID] {
			visible = append(visible, crash)
		}
	}
	return visible
}

func (l *Logger) deleteEntry(id string) error {
	for i := 0; i < logFiles; i++ {
		path := l.path(i)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxLogBytes {
			return errors.New("无法核对运行日志文件")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var out bytes.Buffer
		found := false
		for _, line := range bytes.SplitAfter(data, []byte{'\n'}) {
			key := diagnosticID(bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'}))
			if !found && key == id {
				found = true
				continue
			}
			out.Write(line)
		}
		if !found {
			continue
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
			return errors.New("日志已经变化，请刷新后重试")
		}
		if i == 0 && l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		err = replaceLog(l.dir, path, out.Bytes())
		if i == 0 {
			var openErr error
			l.file, openErr = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if openErr == nil {
				if stat, e := l.file.Stat(); e == nil {
					l.size = stat.Size()
				}
			}
			err = errors.Join(err, openErr)
		}
		return err
	}
	return errors.New("这条运行记录已不存在，请刷新")
}

func replaceLog(dir, path string, data []byte) error {
	f, err := os.CreateTemp(dir, ".log-edit-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if info, e := os.Lstat(path); e == nil && !info.Mode().IsRegular() {
		return errors.New("日志目标已变化")
	}
	return os.Rename(f.Name(), path)
}
