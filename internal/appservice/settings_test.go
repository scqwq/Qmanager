package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
)

func settingsFixture(t *testing.T) (*Media, *catalog.Catalog, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Tencent Files")
	for _, path := range []string{"10001/nt_qq/nt_data/Pic/2026-10/Ori/a.png", "10002/nt_qq/nt_data/Video/2026-10/Thumb/a.png"} {
		if err := testfixture.Image(root, path); err != nil {
			t.Fatal(err)
		}
	}
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, c, root
}

func TestSettingsPersistWithoutImportOrScanAndExplicitImportIsBounded(t *testing.T) {
	s, c, root := settingsFixture(t)
	before, _ := testfixture.Snapshot(root)
	wanted := PathSettings{SourceDirectory: root, BackupDirectory: t.TempDir()}
	saved, err := s.SaveSettings(wanted)
	if err != nil || saved != wanted {
		t.Fatal(saved, err)
	}
	if sources, e := c.Sources(); e != nil || len(sources) != 0 || s.State().Source != nil || s.State().Report != nil || s.State().Watching {
		t.Fatal("saving settings imported or scanned media", sources, e)
	}
	c2, err := catalog.Open(filepath.Join(s.private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewMedia(c2, s.private)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, e := reopened.Settings(); e != nil || got != wanted {
		t.Fatal("settings not retained on reopen", got, e)
	}
	if _, err = s.ImportDefaultSource(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := s.State()
	if len(state.Sources) != 2 || state.Report != nil || state.Watching {
		t.Fatal("default import did not use bounded discovery only", state.Report, state.Watching)
	}
	after, _ := testfixture.Snapshot(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("settings or import changed source")
	}
}

func TestExplicitBackupDefaultWinsOverLastUsedAndClearRestoresFallback(t *testing.T) {
	s, _, root := settingsFixture(t)
	lastUsed, defaultOutput, temporaryOutput := t.TempDir(), t.TempDir(), t.TempDir()
	if _, err := s.RememberBackupDirectory(lastUsed); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Settings(); err != nil || got.BackupDirectory != lastUsed {
		t.Fatal("first selected output did not fill empty default", got, err)
	}
	if path, err := s.BackupDirectory(); err != nil || path != lastUsed {
		t.Fatal(path, err)
	}
	settings := PathSettings{SourceDirectory: root, BackupDirectory: defaultOutput}
	if _, err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RememberBackupDirectory(temporaryOutput); err != nil {
		t.Fatal(err)
	}
	if path, err := s.BackupDirectory(); err != nil || path != defaultOutput {
		t.Fatal("single-use output replaced explicit default", path, err)
	}
	if err := os.Rename(defaultOutput, defaultOutput+"-offline"); err != nil {
		t.Fatal(err)
	}
	if path, err := s.BackupDirectory(); err != nil || path != defaultOutput {
		t.Fatal("offline default silently changed destination", path, err)
	}
	if _, err := s.backupDirectory(defaultOutput); err == nil {
		t.Fatal("offline output accepted for backup")
	}
	if path := DirectoryDialogStart(defaultOutput, temporaryOutput); path != temporaryOutput {
		t.Fatal("picker fallback not available", path)
	}
	settings.BackupDirectory = ""
	if _, err := s.SaveSettings(settings); err != nil {
		t.Fatal("could not clear offline default", err)
	}
	if path, err := s.BackupDirectory(); err != nil || path != temporaryOutput {
		t.Fatal("clearing default lost last used output", path, err)
	}
}

func TestSelectedSourceFillsEmptyDefaultAndKeepsConfiguredValue(t *testing.T) {
	s, _, root := settingsFixture(t)
	if _, err := s.RememberSourceDirectory(root, false); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Settings(); err != nil || got.SourceDirectory != root || s.State().Source != nil {
		t.Fatal("source selection did not remember without scanning", got, err)
	}
	other := filepath.Join(t.TempDir(), "Tencent Files")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RememberSourceDirectory(other, true); err != nil {
		t.Fatal(err)
	}
	if target, _, err := s.RestoreDirectory(); err != nil || target != root {
		t.Fatal("temporary restore target overrode configured default", target, err)
	}
	if _, err := s.SaveSettings(PathSettings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RememberSourceDirectory(other, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Settings(); err != nil || got.SourceDirectory != other {
		t.Fatal("restore selection did not fill empty source default", got, err)
	}
	if _, err := s.SaveSettings(PathSettings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RememberSourceDirectory(t.TempDir(), true); err == nil {
		t.Fatal("non-Tencent Files restore target accepted")
	}
	if got, _ := s.Settings(); got.SourceDirectory != "" {
		t.Fatal("invalid restore selection changed default")
	}
}

func TestBackupPreviewUsesDefaultTencentRootWithoutWritingMedia(t *testing.T) {
	s, c, source, output, row := locationBackup(t, "directory")
	if settings, err := s.Settings(); err != nil || settings.BackupDirectory != output {
		t.Fatal("accepted export did not synchronize unset output default", settings, err)
	}
	before, _ := testfixture.Snapshot(source)
	target := filepath.Join(t.TempDir(), "Tencent Files")
	account := filepath.Join(target, "12345678", "nt_qq", "nt_data")
	if err := os.MkdirAll(account, 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPreference("restore_directory", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSettings(PathSettings{SourceDirectory: account}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewHistoryBackup(context.Background(), row.ID)
	if err != nil || preview.Target != target || preview.TargetNote == "" || preview.RestorePrefix != "12345678/nt_qq/nt_data" {
		t.Fatal("configured source root not mapped to load target", preview, err)
	}
	entries, err := os.ReadDir(account)
	if err != nil || len(entries) != 0 {
		t.Fatal("preview wrote media", err)
	}
	// Choosing Qman-only still sends an empty target; backend must not replace it
	// with the configured default and silently write back to QQ.
	if err = s.RestoreBackup(context.Background(), row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	entries, err = os.ReadDir(account)
	if err != nil || len(entries) != 0 {
		t.Fatal("Qman-only load wrote configured target", err)
	}
	parent := t.TempDir()
	if _, err = s.SaveSettings(PathSettings{SourceDirectory: parent}); err != nil {
		t.Fatal(err)
	}
	if target, note, e := s.RestoreDirectory(); e != nil || target != "" || note == "" {
		t.Fatal("unmapped parent guessed a restore root", target, note, e)
	}
	after, _ := testfixture.Snapshot(source)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source changed")
	}
}

func TestSettingsValidationIsAtomicAndProtectsUnimportedDefaultSource(t *testing.T) {
	s, c, root := settingsFixture(t)
	settings := PathSettings{SourceDirectory: root, BackupDirectory: t.TempDir()}
	if _, err := s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []PathSettings{
		{SourceDirectory: root, BackupDirectory: root},
		{SourceDirectory: root, BackupDirectory: filepath.Dir(root)},
		{SourceDirectory: root, BackupDirectory: filepath.Join(root, "10001", "nt_qq", "nt_data")},
		{SourceDirectory: s.private, BackupDirectory: settings.BackupDirectory},
		{SourceDirectory: root, BackupDirectory: s.private},
		{SourceDirectory: "relative-source", BackupDirectory: ""},
		{SourceDirectory: t.TempDir(), BackupDirectory: filepath.Join(t.TempDir(), "not-created")},
	} {
		if _, err := s.SaveSettings(candidate); err == nil {
			t.Fatal("unsafe or unavailable settings accepted", candidate)
		}
		if got, e := s.Settings(); e != nil || got != settings {
			t.Fatal("invalid save partly changed settings", got, e)
		}
	}
	if _, err := s.RememberBackupDirectory(filepath.Join(root, "10001", "nt_qq", "nt_data")); err == nil {
		t.Fatal("unimported default source not protected from output")
	}
	otherSource := t.TempDir()
	if _, err := c.Add(otherSource, "synthetic-other"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSettings(PathSettings{SourceDirectory: root, BackupDirectory: otherSource}); err == nil {
		t.Fatal("existing other source accepted for output")
	}
	if err := os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	settings.BackupDirectory = t.TempDir()
	if _, err := s.SaveSettings(settings); err != nil {
		t.Fatal("unchanged offline source prevented output edit", err)
	}
	if _, err := s.ImportDefaultSource(context.Background()); err == nil {
		t.Fatal("offline source imported")
	}
}
