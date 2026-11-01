package mediabackup

import (
	"context"
	"os"
)

func VerifyPlacement(ctx context.Context, root *os.Root, path, hash string, size int64) (bool, error) {
	return sameContent(ctx, root, path, hash, size)
}
