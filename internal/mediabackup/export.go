package mediabackup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"strings"
	"time"
)

// ExportFlat publishes ordinary files with no manifest or backup registration.
func ExportFlat(ctx context.Context, target string, items []media.Item, excluded map[string]string, progress func(int, int64)) (result Result, err error) {
	plan := estimate(items, media.Filter{}, excluded)
	if plan.Files == 0 {
		return result, errors.New("没有可导出的文件：所选文件可能已变化、不可读或尚未稳定")
	}
	free, err := storage.Free(target)
	if err != nil || free < uint64(plan.Bytes) {
		return result, errors.New("导出空间不足，或无法查询目标空间")
	}
	stage, err := os.MkdirTemp(target, ".qman-media-stage-")
	if err != nil {
		return result, err
	}
	defer func() {
		if result.Path == "" {
			safeRemove(stage)
			result.StagingRemainder = remainingStage(stage)
		}
	}()
	if err = storage.Private(stage); err != nil {
		return result, fmt.Errorf("设置导出暂存目录访问范围：%w", err)
	}
	root, err := os.OpenRoot(stage)
	if err != nil {
		return result, err
	}
	defer root.Close()
	names := map[string]int{}
	buffer := make([]byte, 1024*1024)
	for _, item := range items {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if exclusion(item, excluded) != "" {
			result.Excluded++
			continue
		}
		name := filepath.Base(filepath.FromSlash(item.RelativePath))
		if !safeRelative(name) {
			return result, errors.New("所选媒体的文件名无法安全导出")
		}
		original := strings.ToLower(name)
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		for names[strings.ToLower(name)] != 0 {
			names[original]++
			name = fmt.Sprintf("%s_%d%s", stem, names[original], ext)
		}
		names[strings.ToLower(name)] = 1
		if err = copyExportFile(ctx, root, name, item, buffer); err != nil {
			return result, fmt.Errorf("导出 %s 失败，未发布导出目录：%w", name, err)
		}
		result.Files++
		result.Bytes += item.Bytes
		progress(result.Files, result.Bytes)
	}
	if err = root.Close(); err != nil {
		return result, err
	}
	prefix := "Export-" + time.Now().Format("20060102-150405")
	for number := 1; number <= 9999; number++ {
		output := filepath.Join(target, fmt.Sprintf("%s-%03d", prefix, number))
		err = publishTree(ctx, stage, output)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("发布导出目录：%w", err)
		}
		result.Path = output
		return result, nil
	}
	return result, errors.New("导出目录编号已用完，请重试")
}

func copyExportFile(ctx context.Context, root *os.Root, name string, item media.Item, buffer []byte) error {
	input, err := media.OpenItem(item)
	if err != nil {
		return err
	}
	defer input.Close()
	out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.CopyBuffer(out, media.CancelReader{Ctx: ctx, Reader: io.LimitReader(input, item.Bytes+1)}, buffer)
	if err = errors.Join(copyErr, out.Sync(), out.Close()); err != nil {
		return err
	}
	if n != item.Bytes || !media.SameItem(input, item) {
		return errors.New("源文件在复制期间发生变化")
	}
	checked, err := media.OpenItem(item)
	if err != nil {
		return err
	}
	return checked.Close()
}
