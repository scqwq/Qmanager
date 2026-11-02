package catalog

import "encoding/json"

// Only called for a user-selected backup. Do not decode all historical media
// manifests or placements when opening the history list.
func (c *Catalog) LegacyBackupImportStatus(id string) (*BackupRestoreStatus, error) {
	var payload string
	err := c.db.QueryRow(`SELECT json_object('written',json_extract(payload,'$.result.written'),
'skipped',json_extract(payload,'$.result.skipped'),'conflicts',json_extract(payload,'$.result.conflicts'),
'fallback',json_extract(payload,'$.result.fallback'),'failed',json_extract(payload,'$.result.failed')) FROM backup_imports WHERE id=?`, id).Scan(&payload)
	if err != nil {
		return nil, err
	}
	var result BackupRestoreStatus
	if err = json.Unmarshal([]byte(payload), &result); err != nil {
		return nil, err
	}
	result.State = "recorded"
	result.Detail = "旧加载结果；执行时间与请求目标未知，以下为当时记录，不代表文件当前仍存在"
	return &result, nil
}

// Called only during single-instance startup recovery, never during a history
// refresh, so an active restore cannot be marked interrupted by a reader.
func (c *Catalog) RecoverBackupRestores() error {
	id, err := c.Preference("active_backup_restore")
	if err != nil || id == "" {
		return err
	}
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE backup_records SET payload=json_set(payload,'$.record.restore_result.state','interrupted',
'$.record.restore_result.detail','上次加载未正常结束，已写入文件可能保留，请核对后重试')
WHERE id=? AND json_extract(payload,'$.record.restore_result.state')='running'`, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM app_preferences WHERE key='active_backup_restore' AND value=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
