package appservice

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"regexp"
	"strings"
)

const appearanceImageLimit = 12 << 20

var appearanceImageID = regexp.MustCompile(`^[a-f0-9]{64}\.(png|jpg)$`)

type AppearanceSettings struct {
	Theme        string `json:"theme"`
	Color        string `json:"color"`
	ImageID      string `json:"image_id"`
	ImageName    string `json:"image_name"`
	Fit          string `json:"fit"`
	PanelOpacity int    `json:"panel_opacity"`
}

type AppearanceImage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type appearanceImage struct {
	AppearanceImage
	data []byte
}

func DefaultAppearance() AppearanceSettings {
	return AppearanceSettings{Theme: "light", Color: "mist", Fit: "cover", PanelOpacity: 100}
}

func validateAppearance(v AppearanceSettings) error {
	colors := map[string]map[string]bool{
		"light": {"mist": true, "gray": true, "violet": true},
		"dark":  {"slate": true, "charcoal": true, "violet": true},
	}
	if !colors[v.Theme][v.Color] || (v.Fit != "cover" && v.Fit != "contain") || v.PanelOpacity < 0 || v.PanelOpacity > 100 {
		return errors.New("外观选项无效，请重新选择主题、颜色和不透明度")
	}
	if v.ImageID != "" && !appearanceImageID.MatchString(v.ImageID) {
		return errors.New("背景图片标识无效，请重新选择图片")
	}
	if len(v.ImageName) > 1024 || strings.ContainsAny(v.ImageName, "/\\\x00") {
		return errors.New("背景图片名称无效")
	}
	return nil
}

func (s *Media) Appearance() (AppearanceSettings, error) {
	v := DefaultAppearance()
	payload, err := s.catalog.Preference("appearance")
	if err != nil {
		return v, err
	}
	if payload == "" {
		return v, nil
	}
	if len(payload) > 4096 || json.Unmarshal([]byte(payload), &v) != nil || validateAppearance(v) != nil {
		return DefaultAppearance(), errors.New("无法读取外观设置，可以恢复默认后重新保存")
	}
	return v, nil
}

// Import only reads the explicitly chosen file. A preview remains in memory;
// no file or preference is published until SaveAppearance succeeds.
func (s *Media) ImportAppearanceImage(path string) (AppearanceImage, error) {
	s.appearanceMu.Lock()
	defer s.appearanceMu.Unlock()
	if !filepath.IsAbs(path) {
		return AppearanceImage{}, errors.New("请使用选择图片按钮")
	}
	f, err := media.OpenRelative(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return AppearanceImage{}, errors.New("无法读取背景图片，请选择本地普通文件")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > appearanceImageLimit {
		return AppearanceImage{}, errors.New("请选择不超过 12 MiB 的 PNG 或 JPEG 图片")
	}
	data, err := io.ReadAll(io.LimitReader(f, appearanceImageLimit+1))
	after, statErr := f.Stat()
	if err != nil || statErr != nil || len(data) > appearanceImageLimit || int64(len(data)) != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		return AppearanceImage{}, errors.New("背景图片在读取时变化或读取失败，请重新选择")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return AppearanceImage{}, errors.New("背景图仅支持静态 PNG 和 JPEG 图片")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 20_000_000 {
		return AppearanceImage{}, errors.New("背景图片尺寸过大，请使用边长不超过 8192、总像素不超过 2000 万的图片")
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return AppearanceImage{}, errors.New("背景图片内容不完整或无法解码")
	}
	// APNG is a PNG container with animation chunks; keep this feature static.
	if format == "png" && animatedPNG(data) {
		return AppearanceImage{}, errors.New("请选择静态 PNG 图片，不支持动态背景")
	}
	ext := ".png"
	if format == "jpeg" {
		ext = ".jpg"
	}
	sum := sha256.Sum256(data)
	v := AppearanceImage{ID: hex.EncodeToString(sum[:]) + ext, Name: filepath.Base(path)}
	if len(v.Name) > 1024 {
		return AppearanceImage{}, errors.New("图片文件名过长，请缩短后重试")
	}
	s.appearanceDraft = &appearanceImage{AppearanceImage: v, data: data}
	return v, nil
}

func animatedPNG(data []byte) bool {
	for at := 8; at+12 <= len(data); {
		n := uint64(data[at])<<24 | uint64(data[at+1])<<16 | uint64(data[at+2])<<8 | uint64(data[at+3])
		if string(data[at+4:at+8]) == "acTL" {
			return true
		}
		if n+12 > uint64(len(data)-at) {
			break
		}
		at += int(n) + 12
	}
	return false
}

func (s *Media) DiscardAppearancePreview() {
	s.appearanceMu.Lock()
	defer s.appearanceMu.Unlock()
	s.appearanceDraft = nil
}

func (s *Media) readAppearanceImage(id string) ([]byte, error) {
	if !appearanceImageID.MatchString(id) {
		return nil, errors.New("无效背景图")
	}
	f, err := media.OpenRelative(s.private, filepath.Join("appearance", id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > appearanceImageLimit {
		return nil, errors.New("背景图不可读取")
	}
	data, err := io.ReadAll(io.LimitReader(f, appearanceImageLimit+1))
	if err != nil || len(data) > appearanceImageLimit {
		return nil, errors.New("背景图读取失败")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.Split(id, ".")[0] {
		return nil, errors.New("背景图已变化，请重新选择")
	}
	return data, nil
}

func (s *Media) SaveAppearance(v AppearanceSettings) (AppearanceSettings, error) {
	s.appearanceMu.Lock()
	defer s.appearanceMu.Unlock()
	if err := validateAppearance(v); err != nil {
		return AppearanceSettings{}, err
	}
	previous, previousErr := s.Appearance()
	// A corrupt preference can be replaced with the defaults or a fresh image.
	if previousErr != nil {
		previous = DefaultAppearance()
	}
	var created bool
	var root *os.Root
	if v.ImageID != "" {
		if s.appearanceDraft != nil && s.appearanceDraft.ID == v.ImageID {
			v.ImageName = s.appearanceDraft.Name
			private, err := os.OpenRoot(s.private)
			if err != nil {
				return AppearanceSettings{}, err
			}
			defer private.Close()
			if err = private.Mkdir("appearance", 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return AppearanceSettings{}, err
			}
			// Reject links before entering this app-owned directory.
			dir, err := media.OpenRelative(s.private, "appearance")
			if err != nil {
				return AppearanceSettings{}, err
			}
			dir.Close()
			root, err = private.OpenRoot("appearance")
			if err != nil {
				return AppearanceSettings{}, err
			}
			defer root.Close()
			f, err := root.OpenFile(v.ImageID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if errors.Is(err, os.ErrExist) {
				if _, err = s.readAppearanceImage(v.ImageID); err != nil {
					return AppearanceSettings{}, err
				}
			} else if err != nil {
				return AppearanceSettings{}, err
			} else {
				_, writeErr := f.Write(s.appearanceDraft.data)
				if writeErr == nil {
					writeErr = f.Sync()
				}
				closeErr := f.Close()
				if writeErr != nil || closeErr != nil {
					_ = root.Remove(v.ImageID)
					return AppearanceSettings{}, errors.New("背景图保存失败，请检查应用数据目录和剩余空间")
				}
				created = true
			}
		} else {
			if v.ImageID != previous.ImageID {
				return AppearanceSettings{}, errors.New("请重新选择背景图片后保存")
			}
			v.ImageName = previous.ImageName
			if _, err := s.readAppearanceImage(v.ImageID); err != nil {
				return AppearanceSettings{}, errors.New("已保存的背景图不可读取，请移除或重新选择图片")
			}
		}
	} else {
		v.ImageName = ""
	}
	payload, err := json.Marshal(v)
	if err == nil {
		err = s.catalog.SetPreference("appearance", string(payload))
	}
	if err != nil {
		if created {
			_ = root.Remove(v.ImageID)
		}
		return AppearanceSettings{}, err
	}
	s.appearanceDraft = nil
	// Only the exact formerly registered app copy can be removed; never the source.
	if previous.ImageID != "" && previous.ImageID != v.ImageID {
		if _, e := s.readAppearanceImage(previous.ImageID); e == nil {
			if private, e := os.OpenRoot(s.private); e == nil {
				_ = private.Remove(filepath.Join("appearance", previous.ImageID))
				private.Close()
			}
		}
	}
	return v, nil
}

func (s *Media) ServeAppearance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/appearance/")
	if !appearanceImageID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	s.appearanceMu.Lock()
	var data []byte
	if s.appearanceDraft != nil && s.appearanceDraft.ID == id {
		data = s.appearanceDraft.data
	} else if saved, err := s.Appearance(); err == nil && saved.ImageID == id {
		data, _ = s.readAppearanceImage(id)
	}
	s.appearanceMu.Unlock()
	if len(data) == 0 {
		http.NotFound(w, r)
		return
	}
	mime := "image/png"
	if strings.HasSuffix(id, ".jpg") {
		mime = "image/jpeg"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}
