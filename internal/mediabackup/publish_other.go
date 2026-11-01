//go:build !windows

package mediabackup

func retryablePublish(error) bool { return false }
