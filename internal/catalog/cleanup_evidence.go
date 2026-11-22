package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"qqarchive/internal/media"
	"time"
)

type cleanupStored struct {
	Seen        string    `json:"seen,omitempty"`
	Bytes       int64     `json:"bytes"`
	Evidence    string    `json:"evidence,omitempty"`
	ManifestRef string    `json:"manifest_ref,omitempty"`
	Record      int       `json:"record,omitempty"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail"`
	Reason      string    `json:"reason,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	Attempts    int       `json:"attempts,omitempty"`
}

type cleanupProof struct {
	Source string     `json:"source"`
	Item   media.Item `json:"item"`
	SHA256 string     `json:"sha256"`
}

type manifestItem struct {
	Item   media.Item
	SHA256 string
	Ref    string
	Record int
}

type briefFile struct {
	Name       string    `json:"n"`
	Instance   string    `json:"i"`
	Identity   string    `json:"v"`
	Bytes      int64     `json:"z"`
	SHA256     string    `json:"h"`
	Kind       string    `json:"k"`
	Month      string    `json:"m"`
	Role       string    `json:"r"`
	Format     string    `json:"f"`
	Status     string    `json:"s"`
	Method     string    `json:"o"`
	Confidence string    `json:"c"`
	Coverage   string    `json:"g"`
	Stability  string    `json:"b"`
	Times      [6]string `json:"t"`
	Excluded   string    `json:"x"`
}

// This is only an internal snapshot projection. Public package validation stays
// in mediabackup, and the snapshot's complete digest is checked before projection.
func manifestItems(payload, ref string) (map[string]manifestItem, error) {
	var snapshot struct {
		Records []struct {
			Dir       int        `json:"d"`
			Directory *string    `json:"directory"`
			File      *briefFile `json:"file"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return nil, err
	}
	dirs := map[int]string{}
	items := map[string]manifestItem{}
	for record, row := range snapshot.Records {
		if row.Directory != nil {
			dirs[row.Dir] = *row.Directory
			continue
		}
		if row.File == nil {
			continue
		}
		f := row.File
		dir, ok := dirs[row.Dir]
		if !ok {
			return nil, errors.New("固定清单目录引用无效")
		}
		times := [6]time.Time{}
		for n, value := range f.Times {
			if value == "" {
				continue
			}
			var err error
			times[n], err = time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, err
			}
		}
		item := media.Item{ID: f.Instance, InstanceID: f.Instance, Identity: f.Identity,
			RelativePath: path.Join(dir, f.Name), Bytes: f.Bytes, Kind: f.Kind, SourceMonth: f.Month,
			Role: f.Role, Format: f.Format, Status: f.Status, Method: f.Method, Confidence: f.Confidence,
			Coverage: f.Coverage, Stability: f.Stability, ModifiedAt: times[0], FirstSeenAt: times[2],
			LastSeenAt: times[3], LastChangedAt: times[4]}
		if f.Times[1] != "" {
			v := times[1]
			item.CreatedAt = &v
		}
		if f.Times[5] != "" {
			v := times[5]
			item.StableObservedAt = &v
		}
		if f.Excluded == "" {
			if item.Status == "" {
				item.Status = "recognized"
			}
			if item.Stability == "" {
				item.Stability = "stable_heuristic"
			}
		}
		if _, exists := items[f.Instance]; exists {
			return nil, errors.New("固定清单实例重复")
		}
		items[f.Instance] = manifestItem{Item: item, SHA256: f.SHA256, Ref: ref, Record: record}
	}
	return items, nil
}

func sameCleanupEvidence(a, b media.Item) bool {
	return a.InstanceID == b.InstanceID && a.Identity == b.Identity &&
		a.RelativePath == b.RelativePath && a.Bytes == b.Bytes && a.ModifiedAt.Equal(b.ModifiedAt) &&
		a.Kind == b.Kind && a.Role == b.Role
}

func (c *Catalog) cleanupReferences(job CleanupJob) (map[string][]manifestItem, error) {
	c.cleanupRefMu.Lock()
	defer c.cleanupRefMu.Unlock()
	if job.ID != "" && c.cleanupRefID == job.ID {
		return c.cleanupRefs, nil
	}
	refs := map[string][]manifestItem{}
	var metadataBytes int
	for _, id := range job.BackupIDs {
		var ref string
		err := c.db.QueryRow("SELECT json_extract(payload,'$.manifest_ref') FROM backup_records WHERE id=?", id).Scan(&ref)
		if isNoRows(err) {
			err = c.db.QueryRow("SELECT digest FROM manifest_snapshots WHERE backup_id=? ORDER BY rowid LIMIT 1", id).Scan(&ref)
			if isNoRows(err) {
				continue
			}
		}
		if err != nil {
			return nil, err
		}
		if ref == "" {
			continue
		}
		payload, err := c.snapshot(ref)
		if err != nil {
			return nil, err
		}
		metadataBytes += len(payload)
		if metadataBytes > 128<<20 {
			return nil, errors.New("所选固定清单过大，请缩小清理范围")
		}
		items, err := manifestItems(payload, ref)
		if err != nil {
			return nil, err
		}
		for id, item := range items {
			refs[id] = append(refs[id], item)
		}
	}
	c.cleanupRefID, c.cleanupRefs = job.ID, refs
	return refs, nil
}

func encodeCleanup(tx *sql.Tx, job CleanupJob, row CleanupItem, refs map[string][]manifestItem) ([]byte, error) {
	stored := cleanupStored{Bytes: row.Item.Bytes, Status: row.Status, Detail: row.Detail, Reason: row.Reason,
		UpdatedAt: row.UpdatedAt, Attempts: row.Attempts}
	if !row.Item.LastSeenAt.IsZero() {
		stored.Seen = row.Item.LastSeenAt.UTC().Format(time.RFC3339Nano)
	}
	for _, ref := range refs[row.Item.InstanceID] {
		if sameCleanupEvidence(ref.Item, row.Item) && ref.SHA256 != "" && (row.SHA256 == "" || row.SHA256 == ref.SHA256) {
			stored.ManifestRef, stored.Record = ref.Ref, ref.Record
			return json.Marshal(stored)
		}
	}
	proof := cleanupProof{Source: job.SourceID, Item: row.Item, SHA256: row.SHA256}
	proof.Item.ID = proof.Item.InstanceID
	proof.Item.CoverID = ""
	proof.Item.LastSeenAt = time.Time{}
	payload, err := json.Marshal(proof)
	if err != nil {
		return nil, err
	}
	ref := ManifestDigest(payload)
	if _, err = tx.Exec("INSERT INTO cleanup_evidence VALUES(?,?) ON CONFLICT(digest) DO NOTHING", ref, string(payload)); err != nil {
		return nil, err
	}
	stored.Evidence = ref
	return json.Marshal(stored)
}

func (c *Catalog) decodeCleanup(payload string, cache map[string]map[int]manifestItem) (CleanupItem, error) {
	var stored cleanupStored
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		return CleanupItem{}, err
	}
	if stored.ManifestRef == "" && stored.Evidence == "" {
		var legacy CleanupItem
		err := json.Unmarshal([]byte(payload), &legacy)
		return legacy, err
	}
	row := CleanupItem{Status: stored.Status, Detail: stored.Detail, Reason: stored.Reason, UpdatedAt: stored.UpdatedAt, Attempts: stored.Attempts}
	if stored.ManifestRef != "" {
		entries, ok := cache[stored.ManifestRef]
		if !ok {
			payload, err := c.snapshot(stored.ManifestRef)
			if err != nil {
				return row, err
			}
			decoded, err := manifestItems(payload, stored.ManifestRef)
			if err != nil {
				return row, err
			}
			entries = map[int]manifestItem{}
			for _, item := range decoded {
				entries[item.Record] = item
			}
			cache[stored.ManifestRef] = entries
		}
		entry, ok := entries[stored.Record]
		if !ok {
			return row, errors.New("清理条目引用无效")
		}
		row.Item, row.SHA256 = entry.Item, entry.SHA256
		if stored.Seen != "" {
			seen, err := time.Parse(time.RFC3339Nano, stored.Seen)
			if err != nil {
				return row, err
			}
			row.Item.LastSeenAt = seen
		}
		return row, nil
	}
	var proofPayload string
	if err := c.db.QueryRow("SELECT payload FROM cleanup_evidence WHERE digest=?", stored.Evidence).Scan(&proofPayload); err != nil {
		return row, err
	}
	if ManifestDigest([]byte(proofPayload)) != stored.Evidence {
		return row, errors.New("清理证据摘要不符")
	}
	var proof cleanupProof
	if err := json.Unmarshal([]byte(proofPayload), &proof); err != nil {
		return row, err
	}
	row.Item, row.SHA256 = proof.Item, proof.SHA256
	if stored.Seen != "" {
		seen, err := time.Parse(time.RFC3339Nano, stored.Seen)
		if err != nil {
			return row, err
		}
		row.Item.LastSeenAt = seen
	}
	return row, nil
}

func (c *Catalog) migrateCleanupEvidence() error {
	var cursor int64
	for {
		rows, err := c.db.Query("SELECT rowid,job,instance,status,payload FROM cleanup_items WHERE rowid>? ORDER BY rowid LIMIT 256", cursor)
		if err != nil {
			return err
		}
		type entry struct {
			seq                      int64
			job, id, status, payload string
		}
		batch := []entry{}
		for rows.Next() {
			var e entry
			if err = rows.Scan(&e.seq, &e.job, &e.id, &e.status, &e.payload); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		jobs := map[string]CleanupJob{}
		references := map[string]map[string][]manifestItem{}
		legacy := map[int64]CleanupItem{}
		for _, e := range batch {
			cursor = e.seq
			var old struct {
				Item *media.Item `json:"item"`
			}
			if err = json.Unmarshal([]byte(e.payload), &old); err != nil {
				return err
			}
			if old.Item == nil {
				continue
			}
			if _, ok := jobs[e.job]; !ok {
				var job CleanupJob
				var jobPayload, jobState string
				err := c.db.QueryRow("SELECT payload,state FROM cleanup_jobs WHERE id=?", e.job).Scan(&jobPayload, &jobState)
				if err == nil {
					err = json.Unmarshal([]byte(jobPayload), &job)
				}
				job.State = jobState
				if err != nil {
					return err
				}
				jobs[e.job] = job
				refs, err := c.cleanupReferences(job)
				if err != nil {
					return err
				}
				references[e.job] = refs
			}
			var row CleanupItem
			if err = json.Unmarshal([]byte(e.payload), &row); err != nil {
				return err
			}
			row.Status = e.status
			legacy[e.seq] = row
		}
		if len(legacy) == 0 {
			continue
		}
		tx, err := c.db.Begin()
		if err != nil {
			return err
		}
		for _, e := range batch {
			row, ok := legacy[e.seq]
			if !ok {
				continue
			}
			payload, e1 := encodeCleanup(tx, jobs[e.job], row, references[e.job])
			if e1 == nil {
				_, e1 = tx.Exec("UPDATE cleanup_items SET payload=? WHERE job=? AND instance=? AND json_type(payload,'$.item')='object'", string(payload), e.job, e.id)
			}
			if e1 == nil && (row.Status == "recycled" || row.Status == "failed" || row.Status == "uncertain" || row.Status == "processing") {
				stamp := ""
				if !row.UpdatedAt.IsZero() {
					stamp = row.UpdatedAt.Format(time.RFC3339Nano)
				}
				_, e1 = tx.Exec("INSERT INTO cleanup_attempts VALUES(?,?,?,?,?,?,?) ON CONFLICT(job,instance,attempt) DO NOTHING", e.job, e.id, 0, row.Status, stamp, row.Detail, row.Reason)
			}
			if e1 != nil {
				tx.Rollback()
				return e1
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
}

func (c *Catalog) restartCleanup(ctx context.Context, job CleanupJob) error {
	if job.State != "interrupted" && job.State != "partial" && job.State != "prepared" {
		return errors.New("此任务不能继续")
	}
	job.State = "preparing"
	job.ExpiresAt = time.Now().UTC()
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, "UPDATE cleanup_jobs SET state='preparing',payload=? WHERE id=?", string(payload), job.ID)
	return err
}

func (c *Catalog) ResumeCleanup(ctx context.Context, id string) (CleanupJob, error) {
	job, err := c.Cleanup(id)
	if err != nil {
		return job, err
	}
	if job.Archived {
		return job, errors.New("预检明细已过期回收，请重新选择")
	}
	if err = c.restartCleanup(ctx, job); err != nil {
		return job, err
	}
	job.State = "preparing"
	return job, nil
}
