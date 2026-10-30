package media

import (
	"bytes"

	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"strings"
	"time"
)

func monthAndRole(relative, kind string) (month, role string) {
	role = "unknown"
	parts := strings.Split(filepath.ToSlash(relative), "/")
	for i, part := range parts[:len(parts)-1] {
		if i == 0 && len(part) == 7 {
			if _, err := time.Parse("2006-01", part); err == nil {
				month = part
			}
		}
		switch strings.ToLower(part) {
		case "oritemp", "thumbtemp", "temp":
			return month, "temporary"
		case "ori":
			role = "original"
		case "thumb":
			role = "preview"
			if kind == "video" {
				role = "cover"
			}
		}
	}
	return
}

// detect recognizes signatures, not complete downloads or fully valid media.
func detect(f *os.File, size int64) (format, status string, preview bool) {
	if size == 0 {
		return "unknown", "empty", false
	}
	header := make([]byte, 64)
	n, err := f.ReadAt(header, 0)
	if err != nil && err != io.EOF {
		return "unknown", "unreadable", false
	}
	b := header[:n]
	format, status = "unknown", "unknown"
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		format = "png"
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		format = "jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")) || bytes.HasPrefix(b, []byte("GIF89a")):
		format = "gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp", "recognized", false
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		return "iso-bmff", "recognized", false
	case bytes.Contains(b, []byte("#!SILK_V3")):
		return "silk", "recognized", false
	case bytes.HasPrefix(b, []byte("#!AMR")):
		return "amr", "recognized", false
	default:
		return
	}
	config, _, err := image.DecodeConfig(io.NewSectionReader(f, 0, min(size, 1024*1024)))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return format, "invalid_header", false
	}
	preview = size <= 16*1024*1024 && int64(config.Width)*int64(config.Height) <= 16_000_000
	return format, "recognized", preview
}
