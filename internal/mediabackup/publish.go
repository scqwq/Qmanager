package mediabackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"time"
)

// Windows can briefly refuse renaming a freshly verified directory while a
// filesystem observer still holds a handle. Retry only a bounded transient
// failure, retaining identity, path isolation, cancellation and no-overwrite.
func publishTree(ctx context.Context, stage, target string) error {
	identity, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = media.Canonical(stage); err != nil {
			return err
		}
		if _, err = media.Canonical(filepath.Dir(target)); err != nil {
			return err
		}
		current, e := os.Lstat(stage)
		if e != nil || !os.SameFile(identity, current) {
			return errors.New("发布暂存目录身份变化")
		}
		if _, e = os.Lstat(target); e == nil {
			return os.ErrExist
		} else if !os.IsNotExist(e) {
			return e
		}
		err = storage.Publish(stage, target)
		if err == nil || !retryablePublish(err) || attempt >= 7 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
