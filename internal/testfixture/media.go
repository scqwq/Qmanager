package testfixture

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

func Image(root, relative string) error {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 9), 120, 255})
		}
	}
	return png.Encode(f, img)
}
func File(root, relative string, body []byte) error {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}
func Snapshot(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		h := sha256.Sum256(b)
		rel, _ := filepath.Rel(root, path)
		out[rel] = hex.EncodeToString(h[:]) + info.ModTime().String() + info.Mode().String()
		return nil
	})
	return out, err
}
