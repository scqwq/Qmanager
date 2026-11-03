//go:build !windows

package diagnostics

func systemCrashes() ([]Crash, string) {
	return []Crash{}, "Windows 崩溃记录仅在 Windows 上可用。"
}
