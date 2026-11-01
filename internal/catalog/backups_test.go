package catalog

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupRegistryIdentityAndPrivateManifest(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	manifest := `{"id":"synthetic","entries":[],"private_observation":"retain"}`
	row := BackupRecord{ID: "synthetic", Path: "synthetic-backup", Manifest: manifest, Digest: ManifestDigest([]byte(manifest)), Status: "available"}
	if err = c.SaveBackup(row); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Backups()
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "private_observation") {
		t.Fatal("full manifest leaked through history projection")
	}
	rows[0].Status = "deleting"
	if err = c.SaveBackup(rows[0]); err != nil {
		t.Fatal(err)
	}
	saved, _ := c.Backup(row.ID)
	if saved.Manifest != manifest {
		t.Fatal("UI projection erased private manifest")
	}
	row.Manifest = `{"id":"synthetic","entries":["different"]}`
	row.Digest = ManifestDigest([]byte(row.Manifest))
	if err = c.SaveBackup(row); err == nil {
		t.Fatal("same ID replaced with different manifest")
	}
	saved, _ = c.Backup(row.ID)
	if saved.Manifest != manifest {
		t.Fatal("collision changed existing row")
	}
	if err = c.RestoreEvidence("synthetic", []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err = c.RestoreEvidence("synthetic", []byte(row.Manifest)); err == nil {
		t.Fatal("observation evidence replaced under same backup ID")
	}
}
