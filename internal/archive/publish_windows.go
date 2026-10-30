package archive

import "golang.org/x/sys/windows"

// The staged package is on the destination volume. Neither replacement nor
// cross-volume copy flags are enabled, so an existing destination causes failure.
func publishNoReplace(staged, output string) error {
	from, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(output)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
