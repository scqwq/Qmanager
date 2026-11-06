package main

import (
	"context"
	"errors"
	"os"
	"qqarchive/internal/catalog"
	"qqarchive/internal/cloud123"
	"time"
)

func (a *App) GetBackupEvents(id string, offset int) ([]catalog.BackupEvent, error) {
	return a.media.BackupEvents(id, offset)
}

func (a *App) GetBackupLogIndex() ([]catalog.BackupRecord, error) {
	rows, err := a.media.BackupLogIndex()
	for i := range rows {
		rows[i].Display = catalog.BackupDisplay{}
	}
	return rows, err
}

func publicBackupDisplay(display catalog.BackupDisplay) catalog.BackupDisplay {
	if display.Cloud != nil {
		cloud := *display.Cloud
		cloud.AccountID, cloud.FileID, cloud.MD5 = "", "", ""
		display.Cloud = &cloud
	}
	return display
}

func (a *App) InspectBackupRecord(id string, explicit bool) (catalog.BackupRecord, error) {
	timeout := 45 * time.Second
	if explicit {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(a.ctx, timeout)
	defer cancel()
	row, err := a.media.InspectBackup(ctx, id, explicit)
	if err != nil {
		return row, err
	}
	if explicit {
		if cloud := row.Display.Cloud; cloud != nil {
			if a.cloud.Status().NeedsCheck {
				_, _ = a.cloud.Refresh(ctx)
			}
			receipt := cloud123.UploadReceipt{AccountID: cloud.AccountID, FileID: cloud.FileID, MD5: cloud.MD5, Size: cloud.Size}
			cloud.State, cloud.Detail = a.cloud.CheckBackupObject(ctx, receipt)
			cloud.CheckedAt = time.Now().UTC()
		}
		row.Display, err = a.media.RecordBackupInspection(row, row.Display.Cloud)
	}
	row.Display = publicBackupDisplay(row.Display)
	return row, err
}

func (a *App) InspectLocalBackupRecord(id string) (catalog.BackupRecord, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	row, err := a.media.InspectBackup(ctx, id, true)
	if err == nil {
		row.Display, err = a.media.RecordBackupInspection(row, nil)
	}
	row.Display = publicBackupDisplay(row.Display)
	return row, err
}

func (a *App) MarkBackupRecord(id string, marks map[string]string) (catalog.BackupDisplay, error) {
	display, err := a.media.MarkBackup(id, marks)
	return publicBackupDisplay(display), err
}

func (a *App) UploadRegisteredBackup(id, parent string) error {
	row, err := a.media.PrepareBackupUpload(a.ctx, id)
	if err != nil {
		return err
	}
	return a.cloud.StartBackupUpload(parent, row.Name, func(ctx context.Context) (string, os.FileInfo, func(), error) {
		return a.media.BackupUploadFile(ctx, row)
	}, func(receipt cloud123.UploadReceipt, uploadErr error) error {
		if uploadErr != nil {
			detail := "上传未完成，请查看本次上传结果"
			if errors.Is(uploadErr, context.Canceled) {
				detail = "已取消上传"
			}
			return a.media.SaveBackupUpload(id, row.Digest, nil, detail)
		}
		cloud := &catalog.BackupCloud{
			AccountID: receipt.AccountID, FileID: receipt.FileID, MD5: receipt.MD5, Size: receipt.Size,
			State: "present", Detail: "上传完成，云端大小与 MD5 核对一致", CheckedAt: time.Now().UTC(),
		}
		if err := a.media.SaveBackupUpload(id, row.Digest, cloud, cloud.Detail); err != nil {
			return errors.New("文件已上传，但未能保存备份关联，请到云盘核对")
		}
		return nil
	})
}
