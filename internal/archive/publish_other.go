//go:build !windows

package archive

import "os"

// Non-Windows publication remains a prototype requiring same-volume hard links.
func publishNoReplace(staged, output string) error { return os.Link(staged, output) }
