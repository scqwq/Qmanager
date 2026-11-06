package appservice

import (
	"encoding/json"
	"errors"
	"qqarchive/internal/storage"
	"time"
)

type dailyReminder struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

func (s *Media) OperationReminder(operation, path string, confirm bool) (string, error) {
	kind, limit := operation, 2
	switch operation {
	case "destination":
		var err error
		kind, err = storage.DestinationKind(path)
		if err != nil || kind == "" {
			return "", err
		}
		limit = 1
	case "source_delete", "backup_delete":
	default:
		return "", errors.New("无效的操作提醒")
	}
	s.reminderMu.Lock()
	defer s.reminderMu.Unlock()
	key := "operation_reminder_" + kind
	value, err := s.catalog.Preference(key)
	if err != nil {
		return "", err
	}
	var reminder dailyReminder
	if value != "" {
		// A damaged preference starts a fresh reminder count.
		_ = json.Unmarshal([]byte(value), &reminder)
	}
	day := time.Now().Format("2006-01-02")
	if reminder.Day != day {
		reminder = dailyReminder{Day: day}
	}
	if reminder.Count >= limit {
		return "", nil
	}
	if confirm {
		reminder.Count++
		data, err := json.Marshal(reminder)
		if err != nil {
			return "", err
		}
		return "", s.catalog.SetPreference(key, string(data))
	}
	return kind, nil
}
