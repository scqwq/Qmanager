package appservice

import (
	"errors"
	"qqarchive/internal/media"
)

func (s *Media) protected() ([]string, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	return s.protectedWithDefault(settings.SourceDirectory)
}
func (s *Media) protectedWithDefault(defaultSource string) ([]string, error) {
	sources, err := s.catalog.Sources()
	if err != nil {
		return nil, err
	}
	roots := []string{s.private}
	if defaultSource != "" {
		if _, e := media.Canonical(defaultSource); e != nil {
			return nil, errors.New("默认 QQ 数据路径发生变化，请在设置中重新选择或清除")
		}
		roots = append(roots, defaultSource)
	}
	if restoreRoot := s.Preference("restore_directory"); restoreRoot != "" {
		if _, e := media.Canonical(restoreRoot); e != nil {
			return nil, errors.New("保存的恢复根发生变化，请重新选择核实")
		}
		roots = append(roots, restoreRoot)
	}
	for _, source := range sources {
		if _, err := media.Canonical(source.Root); err != nil {
			return nil, errors.New("已保存的来源路径发生变化，请重新选择并核实后再备份")
		}
		roots = append(roots, source.Root)
	}
	return roots, nil
}
