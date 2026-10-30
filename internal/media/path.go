package media

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var roots = []struct{ relative, kind string }{{"Pic", "image"}, {"Video", "video"}, {"Emoji/emoji-recv", "emoji"}, {"Ptt", "voice"}}

// Resolve only examines the four allowlisted roots, never the rest of nt_data.
// A selected alias is resolved for display before the user starts a scan.
func Resolve(selected string) (Source, error) {
	a, err := filepath.Abs(selected)
	if err != nil {
		return Source{}, err
	}
	r, err := filepath.EvalSymlinks(a)
	if err != nil {
		return Source{}, errors.New("无法访问所选目录")
	}
	info, err := os.Stat(r)
	if err != nil || !info.IsDir() {
		return Source{}, errors.New("请选择媒体目录")
	}
	s := Source{Selected: a, Resolved: r, Linked: !strings.EqualFold(a, r), Scopes: []Scope{}}
	for _, allowed := range roots {
		if strings.EqualFold(filepath.Base(r), filepath.Base(filepath.FromSlash(allowed.relative))) {
			s.Scopes = append(s.Scopes, Scope{Path: r, Kind: allowed.kind})
			return s, nil
		}
	}
	for _, allowed := range roots {
		p := filepath.Join(r, filepath.FromSlash(allowed.relative))
		info, err := os.Lstat(p)
		if err == nil && info.IsDir() {
			s.Scopes = append(s.Scopes, Scope{Path: p, Kind: allowed.kind, Prefix: allowed.relative})
		}
	}
	if len(s.Scopes) == 0 {
		return Source{}, errors.New("未找到支持的目录：请选择 nt_data、Pic、Video、emoji-recv 或 Ptt")
	}
	s.Scopes = nil
	for _, allowed := range roots {
		s.Scopes = append(s.Scopes, Scope{Path: filepath.Join(r, filepath.FromSlash(allowed.relative)), Kind: allowed.kind, Prefix: allowed.relative})
	}
	return s, nil
}

func localPath(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) || strings.Contains(relative, ":") || strings.Contains(relative, "\x00") {
		return "", errors.New("不允许的相对路径")
	}
	p := filepath.Join(root, relative)
	return p, nil
}

// checkedPath rejects internal aliases, including junctions and ancestor swaps.
func checkedPath(root, relative string) (string, error) {
	p, err := localPath(root, relative)
	if err != nil {
		return "", err
	}
	for current := p; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if isLink(current, info) {
			return "", errors.New("跳过链接或重解析点")
		}
		if strings.EqualFold(current, root) {
			break
		}
		if filepath.Dir(current) == current {
			return "", errors.New("路径越界")
		}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil || !strings.EqualFold(resolved, p) {
		return "", errors.New("源路径发生变化")
	}
	return p, nil
}
