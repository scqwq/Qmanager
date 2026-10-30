package catalog

import "os"

func (c *Catalog) DiskBytes() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-journal"} {
		if info, err := os.Stat(c.path + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}
