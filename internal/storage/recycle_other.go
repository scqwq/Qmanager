//go:build !windows

package storage

import (
	"context"
	"errors"
)

func RecycleFile(context.Context, string, func() error) error {
	return errors.New("仅支持 Windows 回收站")
}
