package catalog

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestRestoreRecoveryAndLegacyProjectionStaySeparateFromPresence(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	manifest := `{"id":"synthetic-status","entries":[]}`
	row := BackupRecord{ID: "synthetic-status", Manifest: manifest, Digest: ManifestDigest([]byte(manifest)), Status: "available"}
	if err = c.SaveBackup(row); err != nil {
		t.Fatal(err)
	}
	if err = c.SaveImport(row.ID, []byte(`{"result":{"written":2,"skipped":1,"conflicts":1,"fallback":0,"failed":0,"placements":[]}}`)); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Backups()
	if err != nil || rows[0].RestoreResult == nil || rows[0].RestoreResult.State != "legacy" {
		t.Fatal(rows, err)
	}
	legacy, err := c.LegacyBackupImportStatus(row.ID)
	if err != nil || legacy.Written != 2 || legacy.Skipped != 1 || legacy.State != "recorded" || !legacy.StartedAt.IsZero() {
		t.Fatal(legacy, err)
	}
	row.RestoreResult = &BackupRestoreStatus{State: "running", StartedAt: time.Now().UTC(), Written: 1}
	if err = c.SaveBackup(row); err != nil {
		t.Fatal(err)
	}
	rows, _ = c.Backups()
	if rows[0].RestoreResult.State != "running" {
		t.Fatal("reader interrupted active restore")
	}
	if err = c.SetPreference("active_backup_restore", row.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.RecoverBackupRestores(); err != nil {
		t.Fatal(err)
	}
	row, err = c.Backup(row.ID)
	if err != nil || row.RestoreResult.State != "interrupted" || row.RestoreResult.Written != 1 || row.OriginalFiles != nil {
		t.Fatal(row.RestoreResult, err)
	}
	payload, _ := json.Marshal(row)
	if !json.Valid(payload) {
		t.Fatal("invalid projection")
	}
}
