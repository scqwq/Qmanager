package mediabackup

import (
	"errors"
	"golang.org/x/sys/windows"
)

func retryablePublish(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
