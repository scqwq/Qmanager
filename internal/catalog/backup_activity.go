package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type BackupMark struct {
	State string    `json:"state"`
	At    time.Time `json:"at"`
}

type BackupCloud struct {
	AccountID string    `json:"account_id"`
	FileID    string    `json:"file_id"`
	MD5       string    `json:"md5"`
	Size      int64     `json:"size"`
	State     string    `json:"state"`
	Detail    string    `json:"detail"`
	CheckedAt time.Time `json:"checked_at"`
}

type BackupDisplay struct {
	Marks map[string]BackupMark `json:"marks,omitempty"`
	Cloud *BackupCloud          `json:"cloud,omitempty"`
}

type BackupEvent struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
}

func (c *Catalog) BackupDisplay(id, digest string) (BackupDisplay, error) {
	var out BackupDisplay
	var payload string
	err := c.db.QueryRow("SELECT payload FROM backup_display WHERE id=? AND digest=?", id, digest).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(payload), &out)
	}
	return out, err
}

// UpdateBackupDisplay changes presentation data without rewriting the backup record.
func (c *Catalog) UpdateBackupDisplay(id, digest string, update func(*BackupDisplay), event BackupEvent) (BackupDisplay, error) {
	tx, err := c.db.Begin()
	var out BackupDisplay
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var current string
	if err = tx.QueryRow("SELECT json_extract(payload,'$.record.digest') FROM backup_records WHERE id=?", id).Scan(&current); err != nil {
		return out, err
	}
	if current != digest {
		return out, errors.New("备份登记已经变化，请重新选择")
	}
	var payload string
	err = tx.QueryRow("SELECT payload FROM backup_display WHERE id=? AND digest=?", id, digest).Scan(&payload)
	if err == nil {
		err = json.Unmarshal([]byte(payload), &out)
	} else if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return out, err
	}
	update(&out)
	data, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec("INSERT INTO backup_display VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET digest=excluded.digest,payload=excluded.payload", id, digest, string(data)); err != nil {
		return out, err
	}
	if event.Action != "" {
		if err = insertBackupEvent(tx, id, digest, event); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func insertBackupEvent(tx *sql.Tx, id, digest string, event BackupEvent) error {
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	if _, err := tx.Exec("INSERT INTO backup_events(backup_id,digest,at,action,detail) VALUES(?,?,?,?,?)", id, digest, event.At.UnixNano(), event.Action, event.Detail); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM backup_events WHERE seq <= (SELECT seq FROM backup_events ORDER BY seq DESC LIMIT 1 OFFSET 100000)")
	return err
}

func (c *Catalog) AddBackupEvent(id, digest string, event BackupEvent) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insertBackupEvent(tx, id, digest, event); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *Catalog) BackupEvents(id, digest string, offset int) ([]BackupEvent, error) {
	rows, err := c.db.Query("SELECT at,action,detail FROM backup_events WHERE backup_id=? AND digest=? ORDER BY at DESC,seq DESC LIMIT 21 OFFSET ?", id, digest, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupEvent{}
	for rows.Next() {
		var event BackupEvent
		var at int64
		if err = rows.Scan(&at, &event.Action, &event.Detail); err != nil {
			return nil, err
		}
		event.At = time.Unix(0, at).UTC()
		out = append(out, event)
	}
	return out, rows.Err()
}
