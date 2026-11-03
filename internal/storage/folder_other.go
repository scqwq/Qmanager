//go:build !windows

package storage

import "errors"

func OpenFolder(string) error {
	return errors.New("当前仅支持在 Windows 文件资源管理器中打开目录")
}
