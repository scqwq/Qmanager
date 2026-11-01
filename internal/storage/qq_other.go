//go:build !windows

package storage

import "errors"

func QQRunning() (bool, error) { return false, errors.New("只在 Windows 验证 QQ 进程状态") }
