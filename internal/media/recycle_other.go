//go:build !windows

package media

import (
	"errors"
	"os"
)

type RecycleGuard struct{ File *os.File }

func (g *RecycleGuard) ReleaseFile() {
	if g.File != nil {
		g.File.Close()
		g.File = nil
	}
}
func (g *RecycleGuard) Close() { g.ReleaseFile() }
func LockForRecycle(Item) (*RecycleGuard, error) {
	return nil, errors.New("仅支持 Windows 媒体回收")
}
