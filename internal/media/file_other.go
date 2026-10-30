//go:build !windows

package media

import (
	"os"
	"time"
)

func isLink(_ string, info os.FileInfo) bool {
	return info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0
}
func openRead(root, relative string) (*os.File, error) {
	p, err := checkedPath(root, relative)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}
func createdAt(os.FileInfo) *time.Time { return nil }
func fileIdentity(*os.File) string     { return "" }
