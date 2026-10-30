//go:build !windows

package storage

import (
	"errors"
	"os"
)

func Private(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}
func Free(string) (uint64, error) { return 0, errors.New("仅验证 Windows 容量预检") }
func Publish(staged, target string) error {
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return errors.New("目标已存在")
	}
	return os.Rename(staged, target)
}
