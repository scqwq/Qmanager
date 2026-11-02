package appservice

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"testing"
)

func appearanceFixture(t *testing.T) *Media {
	t.Helper()
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &Media{catalog: c, private: private}
}

func appearancePicture(t *testing.T, format string) (string, []byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 12, 8))
	img.Set(1, 1, color.RGBA{R: 120, G: 80, B: 30, A: 255})
	var b bytes.Buffer
	var err error
	if format == "jpeg" {
		err = jpeg.Encode(&b, img, nil)
	} else {
		err = png.Encode(&b, img)
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wallpaper."+format)
	if err = os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, b.Bytes()
}

func appearanceGET(s *Media, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeAppearance(w, httptest.NewRequest("GET", "/appearance/"+id, nil))
	return w
}

func TestAppearancePreviewSaveReopenAndRemoveOnlyPrivateCopy(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			s := appearanceFixture(t)
			path, original := appearancePicture(t, format)
			v, err := s.Appearance()
			if err != nil || v != DefaultAppearance() {
				t.Fatal(v, err)
			}
			img, err := s.ImportAppearanceImage(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(s.private, "appearance")); !os.IsNotExist(err) {
				t.Fatal("preview wrote a persistent image", err)
			}
			if got := appearanceGET(s, img.ID); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), original) {
				t.Fatal("preview unavailable", got.Code)
			}
			unchanged, _ := s.Appearance()
			if unchanged != v {
				t.Fatal("preview changed saved preference")
			}
			v.Theme = "dark"
			v.Color = "charcoal"
			v.ImageID = img.ID
			v.ImageName = "forged name"
			v.Fit = "contain"
			v.PanelOpacity = 0
			saved, err := s.SaveAppearance(v)
			if err != nil || saved.ImageName != filepath.Base(path) {
				t.Fatal(saved, err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			c, err := catalog.Open(filepath.Join(s.private, "catalog.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			reopened := &Media{catalog: c, private: s.private}
			got, err := reopened.Appearance()
			if err != nil || got != saved {
				t.Fatal("not retained", got, err)
			}
			if w := appearanceGET(reopened, img.ID); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("saved copy unavailable", w.Code)
			}
			// An original elsewhere remains independent of clearing the app copy.
			if err = os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = reopened.SaveAppearance(DefaultAppearance()); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(s.private, "appearance", img.ID)); !os.IsNotExist(err) {
				t.Fatal("former app copy retained", err)
			}
			if data, _ := os.ReadFile(path); !bytes.Equal(data, original) {
				t.Fatal("source image changed")
			}
			if sources, err := c.Sources(); err != nil || len(sources) != 0 {
				t.Fatal("appearance imported media", err)
			}
		})
	}
}

func TestAppearanceDiscardAndInvalidSaveKeepSavedSettings(t *testing.T) {
	s := appearanceFixture(t)
	path, _ := appearancePicture(t, "png")
	img, err := s.ImportAppearanceImage(path)
	if err != nil {
		t.Fatal(err)
	}
	s.DiscardAppearancePreview()
	if w := appearanceGET(s, img.ID); w.Code != 404 {
		t.Fatal("discard retained preview", w.Code)
	}
	base := DefaultAppearance()
	cases := []AppearanceSettings{base, base, base, base, base, base, base}
	cases[0].Theme = "system"
	cases[1].Color = "slate"
	cases[2].Fit = "stretch"
	cases[3].PanelOpacity = -1
	cases[4].PanelOpacity = 101
	cases[5].ImageID = "../catalog.sqlite"
	cases[6].ImageID = img.ID
	for _, v := range cases {
		if _, err = s.SaveAppearance(v); err == nil {
			t.Fatal("accepted invalid settings", v)
		}
		if saved, _ := s.Appearance(); saved != base {
			t.Fatal("failed save changed preference", saved)
		}
	}
	for _, id := range []string{"catalog.sqlite", "../catalog.sqlite", "..%2Fcatalog.sqlite", img.ID} {
		if w := appearanceGET(s, id); w.Code != 404 {
			t.Fatal("unexpected image exposure", id, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.ServeAppearance(w, httptest.NewRequest(http.MethodPost, "/appearance/"+img.ID, nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestAppearanceImageValidationAndDamagedCopyFallback(t *testing.T) {
	s := appearanceFixture(t)
	path, original := appearancePicture(t, "png")
	for _, content := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), original[:len(original)/2], []byte("GIF89a")} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ImportAppearanceImage(path); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(appearanceImageLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = s.ImportAppearanceImage(path); err == nil {
		t.Fatal("accepted oversized file")
	}
	var b bytes.Buffer
	if err = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8193, 1))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportAppearanceImage(path); err == nil {
		t.Fatal("accepted oversized dimensions")
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	img, err := s.ImportAppearanceImage(path)
	if err != nil {
		t.Fatal(err)
	}
	v := DefaultAppearance()
	v.ImageID = img.ID
	if _, err = s.SaveAppearance(v); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.private, "appearance", img.ID), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := appearanceGET(s, img.ID); w.Code != 404 {
		t.Fatal("served modified image")
	}
	if _, err = s.SaveAppearance(v); err == nil {
		t.Fatal("saved unreadable image")
	}
	if _, err = s.SaveAppearance(DefaultAppearance()); err != nil {
		t.Fatal("could not remove damaged image", err)
	}
}

func TestAppearanceFailedPreferenceWriteDoesNotPublishImage(t *testing.T) {
	s := appearanceFixture(t)
	path, _ := appearancePicture(t, "png")
	img, err := s.ImportAppearanceImage(path)
	if err != nil {
		t.Fatal(err)
	}
	s.catalog.Close()
	v := DefaultAppearance()
	v.ImageID = img.ID
	if _, err = s.SaveAppearance(v); err == nil {
		t.Fatal("saved with closed catalog")
	}
	if _, err = os.Stat(filepath.Join(s.private, "appearance", img.ID)); !os.IsNotExist(err) {
		t.Fatal("failed save left published file", err)
	}
	c, err := catalog.Open(filepath.Join(s.private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if payload, err := c.Preference("appearance"); err != nil || payload != "" {
		t.Fatal("failed save persisted preference", payload, err)
	}
}

func TestAppearanceCorruptPreferenceCanResetAndImageReplacementRetainsSource(t *testing.T) {
	s := appearanceFixture(t)
	if err := s.catalog.SetPreference("appearance", `{"theme":"invalid"}`); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Appearance(); err == nil || v != DefaultAppearance() {
		t.Fatal("corrupt preference did not fall back", v, err)
	}
	if _, err := s.SaveAppearance(DefaultAppearance()); err != nil {
		t.Fatal("reset failed", err)
	}
	path, original := appearancePicture(t, "png")
	first, err := s.ImportAppearanceImage(path)
	if err != nil {
		t.Fatal(err)
	}
	v := DefaultAppearance()
	v.ImageID = first.ID
	if _, err = s.SaveAppearance(v); err != nil {
		t.Fatal(err)
	}
	secondPath, secondOriginal := appearancePicture(t, "jpeg")
	second, err := s.ImportAppearanceImage(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if w := appearanceGET(s, first.ID); w.Code != 200 {
		t.Fatal("preview hid saved image", w.Code)
	}
	v.ImageID = second.ID
	if _, err = s.SaveAppearance(v); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.private, "appearance", first.ID)); !os.IsNotExist(err) {
		t.Fatal("replacement kept former private copy", err)
	}
	for p, wanted := range map[string][]byte{path: original, secondPath: secondOriginal} {
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, wanted) {
			t.Fatal("source changed", err)
		}
	}
}
