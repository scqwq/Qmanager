package media

import (
	"errors"
	"time"
)

func ValidateFilter(f Filter) error {
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
	return nil
}
