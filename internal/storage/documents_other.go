//go:build !windows

package storage

import "errors"

func DocumentsDirectory() (string, error) {
	return "", errors.New("仅支持Windows常用目录查找")
}
