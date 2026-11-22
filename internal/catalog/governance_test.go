package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qqarchive/internal/media"
)

func governanceFixture(t *testing.T) (*Catalog, media.Source, media.Item, BackupRecord) {
	t.Helper()
	c, err := Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	saved, err := c.Add(t.TempDir(), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	item := media.Item{ID: "instance", InstanceID: "instance", Identity: "synthetic-identity", RelativePath: "Pic/2025-01/Ori/a.png", Bytes: 10, Kind: "image", Role: "original", Format: "png", Status: "recognized", Stability: "stable_heuristic", ModifiedAt: at, FirstSeenAt: at}
	manifest := `{"header":{"id":"backup"},"records":[{"d":0,"directory":"Pic/2025-01/Ori"},{"d":0,"file":{"n":"a.png","i":"instance","v":"synthetic-identity","z":10,"h":"` + strings.Repeat("a", 64) + `","k":"image","r":"original","f":"png","t":["2025-01-02T03:04:05Z","","2025-01-02T03:04:05Z","","",""]}}]}`
	row := BackupRecord{ID: "backup", Manifest: manifest, Digest: ManifestDigest([]byte(manifest)), Status: "deleted"}
	if err = c.SaveBackup(row); err != nil {
		t.Fatal(err)
	}
	return c, media.Source{ID: saved.ID, Name: saved.Name, Resolved: saved.Root}, item, row
}

func TestSharedManifestAndCleanupReferencesSurviveHistoryDeletion(t *testing.T) {
	c, source, item, backup := governanceFixture(t)
	ctx := context.Background()
	importPayload := []byte(`{"manifest":` + backup.Manifest + `,"result":{"placements":[]}}`)
	if err := c.SaveImport(backup.ID, importPayload); err != nil {
		t.Fatal(err)
	}
	if err := c.RestoreEvidence(backup.ID, []byte(backup.Manifest)); err != nil {
		t.Fatal(err)
	}
	job, err := c.CreateCleanup(ctx, source, media.Filter{}, []media.Item{item}, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	row := CleanupItem{Item: item, SHA256: strings.Repeat("a", 64), Status: "ready"}
	if err = c.SaveCleanupItem(ctx, job, row); err != nil {
		t.Fatal(err)
	}
	var count int
	c.db.QueryRow("SELECT count(*) FROM manifest_snapshots").Scan(&count)
	if count != 1 {
		t.Fatalf("same snapshot stored %d times", count)
	}
	var payload string
	c.db.QueryRow("SELECT payload FROM cleanup_items WHERE job=?", job.ID).Scan(&payload)
	if strings.Contains(payload, "relative_path") || !strings.Contains(payload, "manifest_ref") {
		t.Fatalf("cleanup duplicated the backup item: %s", payload)
	}
	if err = c.CleanupState(job.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.DeleteBackupHistory(ctx, backup.ID, backup.Digest); err != nil {
		t.Fatal(err)
	}
	items, err := c.CleanupItems(job.ID, 0, 10)
	if err != nil || len(items) != 1 || !sameCleanupEvidence(items[0].Item, item) || items[0].SHA256 != row.SHA256 {
		t.Fatalf("lost cleanup evidence: %+v %v", items, err)
	}
	imported, err := c.Import(backup.ID)
	if err != nil || !strings.Contains(string(imported), `"manifest":`) {
		t.Fatal("lost imported evidence", err)
	}
	if _, err = c.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.snapshot(backup.Digest); err != nil {
		t.Fatal("referenced snapshot collected", err)
	}
	wrong := backup
	wrong.Manifest = `{"header":{"id":"backup"},"records":[]}`
	wrong.Digest = ManifestDigest([]byte(wrong.Manifest))
	if err = c.SaveBackup(wrong); err == nil {
		t.Fatal("forgotten registry bypassed an existing snapshot identity")
	}
}

func TestLegacyEvidenceMigrationPreservesUnknownTimesAndAudit(t *testing.T) {
	c, source, item, backup := governanceFixture(t)
	job, err := c.CreateCleanup(context.Background(), source, media.Filter{}, []media.Item{item}, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := CleanupItem{Item: item, SHA256: strings.Repeat("a", 64), Status: "recycled", Detail: "legacy result"}
	b, _ := json.Marshal(old)
	legacy, _ := json.Marshal(storedBackup{Record: backup, Manifest: backup.Manifest})
	if _, err = c.db.Exec("UPDATE backup_records SET payload=? WHERE id=?", string(legacy), backup.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.db.Exec("UPDATE cleanup_items SET status='recycled',payload=? WHERE job=?", string(b), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c.db.Exec("DELETE FROM app_preferences WHERE key='evidence_storage_version'"); err != nil {
		t.Fatal(err)
	}
	path := c.path
	c.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	items, err := again.CleanupItems(job.ID, 0, 10)
	if err != nil || len(items) != 1 || items[0].Status != "recycled" || !items[0].UpdatedAt.IsZero() || items[0].Detail != old.Detail {
		t.Fatalf("legacy history changed: %+v %v", items, err)
	}
	got, err := again.Backup(backup.ID)
	if err != nil || got.Manifest != backup.Manifest {
		t.Fatal("legacy snapshot changed", err)
	}
}

func TestExpiredPreviewsAndProcessedNotificationRetention(t *testing.T) {
	c, source, item, backup := governanceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	preview := func(executed bool) string {
		t.Helper()
		job, err := c.CreateCleanup(ctx, source, media.Filter{}, []media.Item{item}, backup.ID)
		if err != nil {
			t.Fatal(err)
		}
		job.State = "prepared"
		job.Executed = executed
		payload, _ := json.Marshal(job)
		if _, err = c.db.Exec("UPDATE cleanup_jobs SET state='prepared',payload=? WHERE id=?", string(payload), job.ID); err != nil {
			t.Fatal(err)
		}
		return job.ID
	}
	expired, executed := preview(false), preview(true)
	interrupted := preview(false)
	if err := c.CleanupState(interrupted, "interrupted"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{expired, executed, interrupted} {
		job, err := c.Cleanup(id)
		if err != nil {
			t.Fatal(err)
		}
		job.ExpiresAt = now.Add(-8 * 24 * time.Hour)
		payload, _ := json.Marshal(job)
		if _, err = c.db.Exec("UPDATE cleanup_jobs SET payload=? WHERE id=?", string(payload), id); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-31 * 24 * time.Hour)
	if err := c.RawEvent(source.ID, "Pic/a", 1, old); err != nil {
		t.Fatal(err)
	}
	boundary, _ := c.EventBoundary(source.ID)
	if err := c.ProcessedEvents(ctx, source.ID, boundary, nil, true); err != nil {
		t.Fatal(err)
	}
	if err := c.RawEvent(source.ID, "Pic/b", 1, old); err != nil {
		t.Fatal(err)
	}
	result, err := c.Sweep(ctx, now)
	if err != nil || result.Previews != 2 || result.Notifications != 1 {
		t.Fatalf("unexpected sweep: %+v %v", result, err)
	}
	job, err := c.Cleanup(expired)
	if err != nil || !job.Archived || job.Total != 1 {
		t.Fatal("expired preview summary lost", job, err)
	}
	rows, err := c.CleanupItems(expired, 0, 10)
	if err != nil || len(rows) != 0 {
		t.Fatal("expired details retained", err)
	}
	rows, err = c.CleanupItems(executed, 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatal("executed audit removed", err)
	}
	var pending int
	c.db.QueryRow("SELECT count(*) FROM watch_events WHERE processed=0").Scan(&pending)
	if pending != 1 {
		t.Fatal("unprocessed notification deleted")
	}
}

func TestCleanupEvidenceDeduplicatesAndKeepsAttempts(t *testing.T) {
	c, source, item, _ := governanceFixture(t)
	ctx := context.Background()
	for n := 0; n < 2; n++ {
		item.LastSeenAt = time.Date(2025, 1, 2, 3, 4, 5+n, 0, time.UTC)
		job, err := c.CreateCleanup(ctx, source, media.Filter{}, []media.Item{item})
		if err != nil {
			t.Fatal(err)
		}
		row := CleanupItem{Item: item, SHA256: strings.Repeat("b", 64), Status: "processing"}
		if err = c.SaveCleanupItem(ctx, job, row); err != nil {
			t.Fatal(err)
		}
		row.Status = "failed"
		if err = c.SaveCleanupItem(ctx, job, row); err != nil {
			t.Fatal(err)
		}
		items, err := c.CleanupItems(job.ID, 0, 10)
		if err != nil || items[0].Attempts != 1 || !items[0].Item.LastSeenAt.Equal(item.LastSeenAt) {
			t.Fatal("attempt state lost", err)
		}
	}
	var count int
	c.db.QueryRow("SELECT count(*) FROM cleanup_evidence").Scan(&count)
	if count != 2 {
		t.Fatalf("immutable item and digest duplicated across tasks: %d", count)
	}
	// Free pages must contribute to every capacity reservation.
	if _, err := c.db.Exec("CREATE TABLE transient(data); INSERT INTO transient VALUES(zeroblob(1048576)); DROP TABLE transient"); err != nil {
		t.Fatal(err)
	}
	before, err := c.Usage(ctx)
	if err != nil || before.Reusable == 0 {
		t.Fatal("missing reusable page accounting", err)
	}
	if _, err = c.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(c.path)
	if after.Size() > before.Bytes {
		t.Fatal("compact grew the database")
	}
}
