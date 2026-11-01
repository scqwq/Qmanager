// Package catalog stores application-generated evidence, never QQ databases.
package catalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/storage"
	"time"
)

type Catalog struct {
	db   *sql.DB
	path string
}
type SavedSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}
type Record struct {
	Item    media.Item
	Samples int
	Present bool
	Seen    time.Time
}

func Open(path string) (*Catalog, error) {
	p, err := media.Canonical(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA trusted_schema=OFF;
CREATE TABLE IF NOT EXISTS qman_meta(version INTEGER NOT NULL);
INSERT INTO qman_meta SELECT 1 WHERE NOT EXISTS(SELECT 1 FROM qman_meta);
CREATE TABLE IF NOT EXISTS sources(id TEXT PRIMARY KEY,name TEXT NOT NULL,root TEXT NOT NULL UNIQUE COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS instances(id TEXT PRIMARY KEY,source TEXT NOT NULL REFERENCES sources(id),path TEXT NOT NULL,payload TEXT NOT NULL,present INTEGER NOT NULL,samples INTEGER NOT NULL,seen TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS instance_source ON instances(source,present,path);
CREATE TABLE IF NOT EXISTS events(id INTEGER PRIMARY KEY,source TEXT NOT NULL,instance TEXT NOT NULL,at TEXT NOT NULL,kind TEXT NOT NULL,detail TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS coverage(id INTEGER PRIMARY KEY,source TEXT NOT NULL,start TEXT NOT NULL,end TEXT NOT NULL,reason TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS source_state(source TEXT PRIMARY KEY,scanned TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS restored_backups(id TEXT PRIMARY KEY,manifest TEXT NOT NULL,restored TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS watch_events(id INTEGER PRIMARY KEY,source TEXT NOT NULL,path TEXT NOT NULL,action INTEGER NOT NULL,at TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err = db.QueryRow("SELECT version FROM qman_meta").Scan(&version); err != nil || version != 1 {
		db.Close()
		return nil, errors.New("索引版本不受支持")
	}
	c := &Catalog{db: db, path: p}
	if err = c.initCache(); err != nil {
		db.Close()
		return nil, err
	}
	if err = c.initBackups(); err != nil {
		db.Close()
		return nil, err
	}
	if err = c.initCleanup(); err != nil {
		db.Close()
		return nil, err
	}
	return c, nil
}
func (c *Catalog) Close() error { return c.db.Close() }
func (c *Catalog) Sources() ([]SavedSource, error) {
	rows, err := c.db.Query("SELECT id,name,root FROM sources ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedSource{}
	for rows.Next() {
		var s SavedSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Root); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (c *Catalog) Add(root, name string) (SavedSource, error) {
	var s SavedSource
	err := c.db.QueryRow("SELECT id,name,root FROM sources WHERE root=?", root).Scan(&s.ID, &s.Name, &s.Root)
	if err == nil {
		return s, nil
	}
	if err != sql.ErrNoRows {
		return s, err
	}
	var count int
	if err = c.db.QueryRow("SELECT count(*) FROM sources").Scan(&count); err != nil {
		return s, err
	}
	if count >= 32 {
		return s, errors.New("最多保存 32 个媒体来源")
	}
	if name == "" {
		name = filepath.Base(root)
	}
	s = SavedSource{uuid.NewString(), name, root}
	_, err = c.db.Exec("INSERT INTO sources VALUES(?,?,?)", s.ID, s.Name, s.Root)
	return s, err
}
func (c *Catalog) Rename(id, name string) error {
	if len([]rune(name)) < 1 || len([]rune(name)) > 60 {
		return errors.New("来源名称需为 1～60 字")
	}
	_, err := c.db.Exec("UPDATE sources SET name=? WHERE id=?", name, id)
	return err
}
func (c *Catalog) Load(id string) ([]Record, error) { return c.loadRecords(id, false) }
func (c *Catalog) Gap(id, reason string, start, end time.Time) error {
	_, err := c.db.Exec("INSERT INTO coverage(source,start,end,reason) VALUES(?,?,?,?)", id, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), reason)
	return err
}

// Apply retains every instance/version. Baseline and discontinuous scans never
// claim a download time; only a matching native creation event is live evidence.
func (c *Catalog) Apply(ctx context.Context, id string, r *media.Report, initial bool, created map[string]time.Time, covered bool, now time.Time) error {
	if info, err := os.Stat(c.path); err == nil && info.Size() > 512*1024*1024 {
		return errors.New("索引达到 512 MiB 上限，请备份后维护索引")
	}
	previous, err := c.activeForReport(ctx, id, r)
	if err != nil {
		return err
	}
	active := map[string]Record{}
	identities := map[string][]Record{}
	known := len(previous) > 0
	var baselineCount int
	if err = c.db.QueryRow("SELECT count(*) FROM source_state WHERE source=?", id).Scan(&baselineCount); err != nil {
		return err
	}
	known = known || baselineCount > 0
	if !known {
		var baseline int
		if err = c.db.QueryRowContext(ctx, "SELECT count(*) FROM source_baseline WHERE source=?", id).Scan(&baseline); err != nil {
			return err
		}
		known = baseline > 0
	}
	if !known {
		var prior int
		if err = c.db.QueryRow("SELECT count(*) FROM (SELECT 1 FROM instances WHERE source=? LIMIT 1)", id).Scan(&prior); err != nil {
			return err
		}
		known = prior > 0
	}
	currentPaths := map[string]bool{}
	for _, item := range r.Items {
		currentPaths[item.RelativePath] = true
	}
	for _, p := range previous {
		if p.Present {
			active[p.Item.RelativePath] = p
			if p.Item.Identity != "" {
				identities[p.Item.Identity] = append(identities[p.Item.Identity], p)
			}
		}
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var eventCount int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&eventCount); err != nil {
		return err
	}
	if eventCount >= 1000000 {
		return errors.New("观察历史达到容量上限")
	}
	event := func(instance, kind, detail string) error {
		eventCount++
		if eventCount > 1000000 {
			return errors.New("观察历史达到容量上限")
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO events(source,instance,at,kind,detail) VALUES(?,?,?,?,?)", id, instance, now.UTC().Format(time.RFC3339Nano), kind, detail)
		return e
	}
	used := map[string]bool{}
	observedIDs := []string{}
	for i := range r.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		item := &r.Items[i]
		old, exists := active[item.RelativePath]
		unchanged := exists && item.Identity != "" && old.Item.Identity == item.Identity && old.Item.Bytes == item.Bytes && old.Item.ModifiedAt.Equal(item.ModifiedAt)
		renamed := false
		if !exists && item.Identity != "" && len(identities[item.Identity]) == 1 {
			candidate := identities[item.Identity][0]
			// Do not merge hard links or a same-identity copy that is still present.
			stillPresent := currentPaths[candidate.Item.RelativePath]
			if !r.Complete || (r.Partial && !media.Covers(r.Covered, candidate.Item.RelativePath)) {
				stillPresent = true
			}
			if !stillPresent && !used[candidate.Item.InstanceID] && candidate.Item.Bytes == item.Bytes && candidate.Item.ModifiedAt.Equal(item.ModifiedAt) {
				old = candidate
				unchanged = true
				renamed = true
			}
		}
		samples := 1
		if unchanged {
			item.InstanceID = old.Item.InstanceID
			item.FirstSeenAt = old.Item.FirstSeenAt
			item.LastChangedAt = old.Item.LastChangedAt
			item.Method = old.Item.Method
			item.Confidence = old.Item.Confidence
			item.Coverage = old.Item.Coverage
			item.StableObservedAt = old.Item.StableObservedAt
			samples = old.Samples
			if now.Sub(old.Seen) >= time.Second && now.After(old.Seen) {
				samples++
			}
			if samples > 3 {
				samples = 3
			}
			if old.Item.Role != item.Role {
				samples = 1
				item.StableObservedAt = nil
			}
			if renamed {
				r.RemovedPaths = append(r.RemovedPaths, old.Item.RelativePath)
				item.LastChangedAt = now.UTC()
				if err = event(item.InstanceID, "rename", "volume_file_identity"); err != nil {
					return err
				}
			}
		} else {
			if exists {
				if _, err = tx.ExecContext(ctx, "UPDATE instances SET present=0 WHERE id=?", old.Item.InstanceID); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "DELETE FROM media_cache WHERE id=?", old.Item.InstanceID); err != nil {
					return err
				}
				if err = event(old.Item.InstanceID, "superseded", "same_path_new_version"); err != nil {
					return err
				}
			}
			item.InstanceID = rand.Text()
			item.FirstSeenAt = now.UTC()
			item.LastChangedAt = now.UTC()
			item.Confidence = "observation_only"
			item.Coverage = "gap"
			item.Method = "rescan_after_gap"
			if !known {
				item.Method = "baseline"
			}
			if t, ok := created[item.RelativePath]; ok {
				item.Method = "live_event"
				item.FirstSeenAt = t.UTC()
				item.Coverage = "live"
			} else if initial && !known {
				item.Method = "baseline"
			} else if covered {
				item.Coverage = "periodic_reconciliation"
			}
			if item.Method == "baseline" {
				item.Coverage = "baseline_scan"
			}
			if err = event(item.InstanceID, "seen", item.Method); err != nil {
				return err
			}
		}
		item.LastSeenAt = now.UTC()
		if item.Status == "unreadable" || item.Status == "changed" {
			samples = 0
		}
		item.Stability = "pending"
		if item.Bytes > 0 && item.Role != "temporary" && item.Status == "recognized" && samples >= 3 {
			item.Stability = "stable_heuristic"
			if item.StableObservedAt == nil {
				t := now.UTC()
				item.StableObservedAt = &t
				if err = event(item.InstanceID, "stable", "three_samples_at_least_one_second_apart_not_download_proof"); err != nil {
					return err
				}
			}
		} else {
			item.StableObservedAt = nil
		}
		item.ID = item.InstanceID
		used[item.InstanceID] = true
		if unchanged && !renamed && old.Samples == samples && old.Item.Format == item.Format && old.Item.Status == item.Status && old.Item.Preview == item.Preview && old.Item.Stability == item.Stability {
			observedIDs = append(observedIDs, item.InstanceID)
			continue
		}
		payload, e := json.Marshal(item)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO instances VALUES(?,?,?,?,1,?,?) ON CONFLICT(id) DO UPDATE SET path=excluded.path,payload=excluded.payload,present=1,samples=excluded.samples,seen=excluded.seen`, item.InstanceID, id, item.RelativePath, string(payload), samples, now.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		if err = project(tx, id, *item, payload); err != nil {
			return err
		}
		r.IndexedUpdates++
	}
	if err = touch(tx, observedIDs, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if r.Complete {
		for _, old := range active {
			if !used[old.Item.InstanceID] && (!r.Partial || media.Covers(r.Covered, old.Item.RelativePath)) {
				if _, err = tx.ExecContext(ctx, "UPDATE instances SET present=0 WHERE id=?", old.Item.InstanceID); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "DELETE FROM media_cache WHERE id=?", old.Item.InstanceID); err != nil {
					return err
				}
				r.IndexedUpdates++
				r.RemovedPaths = append(r.RemovedPaths, old.Item.RelativePath)
				if err = event(old.Item.InstanceID, "missing", "complete_reconciliation"); err != nil {
					return err
				}
			}
		}
	}
	if r.Complete && !r.Partial {
		if _, err = tx.ExecContext(ctx, "INSERT INTO source_state VALUES(?,?) ON CONFLICT(source) DO UPDATE SET scanned=excluded.scanned", id, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if r.Complete && (!r.Partial || r.Quick && r.NamesEnumerated) {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO source_baseline VALUES(?,?)", id, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	reportCopy := *r
	reportCopy.Items = nil
	reportCopy.Covered = nil
	reportCopy.RemovedPaths = nil
	reportPayload, e := json.Marshal(reportCopy)
	if e != nil {
		return e
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO source_reports VALUES(?,?) ON CONFLICT(source) DO UPDATE SET payload=excluded.payload", id, string(reportPayload)); err != nil {
		return err
	}
	return tx.Commit()
}

type History struct {
	At     string `json:"at"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

func (c *Catalog) History(instance string) ([]History, error) {
	rows, err := c.db.Query("SELECT at,kind,detail FROM events WHERE instance=? ORDER BY id LIMIT 1000", instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []History{}
	for rows.Next() {
		var h History
		if err = rows.Scan(&h.At, &h.Kind, &h.Detail); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (c *Catalog) RestoreEvidence(id string, payload []byte) error {
	if len(payload) > 128*1024*1024 {
		return errors.New("备份证据超过上限")
	}
	var previous string
	err := c.db.QueryRow("SELECT manifest FROM restored_backups WHERE id=?", id).Scan(&previous)
	if err == nil && previous != string(payload) {
		return errors.New("备份编号已存在但清单不同")
	}
	if err != nil && !isNoRows(err) {
		return err
	}
	if isNoRows(err) {
		if info, e := os.Stat(c.path); e == nil && info.Size()+int64(len(payload)) > 512*1024*1024 {
			return errors.New("索引容量不足以保存观察证据；已复制媒体仍保留")
		}
	}
	_, err = c.db.Exec("INSERT INTO restored_backups VALUES(?,?,?) ON CONFLICT(id) DO NOTHING", id, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (c *Catalog) Export(ctx context.Context, parent string) (string, error) {
	target, err := media.Canonical(parent)
	if err != nil {
		return "", err
	}
	if media.Overlap(filepath.Dir(c.path), target) {
		return "", errors.New("请选择与应用数据分离的索引备份位置")
	}
	stage, err := os.MkdirTemp(target, ".qman-index-stage-")
	if err != nil {
		return "", err
	}
	defer func() {
		p := filepath.Join(stage, "index.sqlite")
		if _, e := media.Canonical(p); e == nil {
			os.Remove(p)
		}
		os.Remove(stage)
	}()
	if err = storage.Private(stage); err != nil {
		return "", err
	}
	if _, err = c.db.ExecContext(ctx, "VACUUM INTO ?", filepath.Join(stage, "index.sqlite")); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	output := filepath.Join(target, "Qman-index-"+time.Now().Format("20060102-150405")+"-"+rand.Text()[:8]+".sqlite")
	if _, err = media.Canonical(target); err != nil {
		return "", err
	}
	if err = storage.Publish(filepath.Join(stage, "index.sqlite"), output); err != nil {
		return "", err
	}
	return output, nil
}
