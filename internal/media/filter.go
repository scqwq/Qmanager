package media

import (
	"errors"
	"time"
)

func ValidateFilter(f Filter) error {
	if f.SourceMonth != "" && f.SourceMonth != "__unknown__" {
		if _, err := time.Parse("2006-01", f.SourceMonth); err != nil {
			return errors.New("来源月份格式无效")
		}
	}
	for _, value := range []string{f.MonthFrom, f.MonthTo} {
		if value != "" {
			if _, err := time.Parse("2006-01", value); err != nil {
				return errors.New("月份范围格式无效")
			}
		}
	}
	if f.MonthFrom != "" && f.MonthTo != "" && f.MonthFrom > f.MonthTo {
		return errors.New("开始月份不能晚于结束月份")
	}
	_, _, err := f.RecordRange()
	return err
}

// MatchesSourceMonth compares the QQ directory label independently of the view.
func (f Filter) MatchesSourceMonth(month string) bool {
	if f.SourceMonth == "__unknown__" {
		return month == ""
	}
	return f.SourceMonth == "" || f.SourceMonth == month
}

// RecordRange interprets both legacy dates and local date-hour values. The end
// bound is exclusive, so the chosen end date or hour is included in full.
func (f Filter) RecordRange() (start, end time.Time, err error) {
	start, err = recordBoundary(f.From, false)
	if err != nil {
		return start, end, err
	}
	end, err = recordBoundary(f.To, true)
	if err == nil && !start.IsZero() && !end.IsZero() && !start.Before(end) {
		err = errors.New("开始时间不能晚于结束时间")
	}
	return start, end, err
}

func recordBoundary(value string, end bool) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	const dateLayout = "2006-01-02"
	const hourLayout = "2006-01-02T15"
	layout := dateLayout
	if len(value) == len(hourLayout) {
		layout = hourLayout
	}
	t, err := time.ParseInLocation(layout, value, time.Local)
	if err != nil || (layout == hourLayout && t.Format(layout) != value) {
		return time.Time{}, errors.New("首次记录时间格式无效")
	}
	if end {
		if layout == hourLayout {
			return t.Add(time.Hour), nil
		}
		return t.AddDate(0, 0, 1), nil
	}
	return t, nil
}
