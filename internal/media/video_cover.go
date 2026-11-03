package media

import (
	"path"
	"strings"
	"time"
)

// VisibleRole leaves files intact while hiding standalone video covers unless
// explicitly requested. Temporary resources keep their existing role.
func VisibleRole(kind, role, selectedRole string) bool {
	return kind != "video" || role != "cover" || selectedRole == "cover"
}

// VideoCoverPath derives a candidate only for the observed QQ naming convention.
// It does not claim that the identifier is a content hash or a message identity.
func VideoCoverPath(item Item) string {
	if item.Kind != "video" || item.Role != "original" || item.Format != "iso-bmff" || item.Status != "recognized" {
		return ""
	}
	p := strings.ReplaceAll(item.RelativePath, "\\", "/")
	dir, file := path.Split(p)
	if !strings.EqualFold(path.Base(path.Clean(dir)), "ori") || !strings.EqualFold(path.Ext(file), ".mp4") {
		return ""
	}
	monthDir := path.Dir(path.Clean(dir))
	if path.Base(monthDir) != item.SourceMonth {
		return ""
	}
	if _, err := time.Parse("2006-01", item.SourceMonth); err != nil {
		return ""
	}
	id := strings.TrimSuffix(file, path.Ext(file))
	if len(id) != 32 || strings.IndexFunc(id, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F')
	}) >= 0 {
		return ""
	}
	return path.Join(monthDir, "Thumb", id+"_0.png")
}

// MatchVideoCovers requires a unique candidate, in the same source/view supplied
// by the caller. Preview remains the cover's own decoded-image capability.
func MatchVideoCovers(videos, candidates []Item) map[string]Item {
	byPath := make(map[string][]Item)
	for _, item := range candidates {
		if item.Kind == "video" && item.Role == "cover" {
			key := strings.ToLower(strings.ReplaceAll(item.RelativePath, "\\", "/"))
			byPath[key] = append(byPath[key], item)
		}
	}
	out := make(map[string]Item)
	for _, video := range videos {
		p := VideoCoverPath(video)
		if p == "" {
			continue
		}
		matches := byPath[strings.ToLower(p)]
		if len(matches) == 1 {
			cover := matches[0]
			if cover.SourceMonth == video.SourceMonth && cover.Preview && cover.Status == "recognized" && cover.Bytes > 0 {
				out[video.ID] = cover
			}
		}
	}
	return out
}
