//go:build !windows

package storage

func DestinationKind(path string) (string, error) { return "", nil }
