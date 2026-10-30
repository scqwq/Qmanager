//go:build !windows

package watch

import (
	"context"
	"qqarchive/internal/media"
	"time"
)

type Event struct {
	Path   string
	Action uint32
	At     time.Time
	Gap    bool
}
type Watch struct {
	Events   chan Event
	Complete bool
}

func Start(context.Context, media.Source) (*Watch, error) {
	return &Watch{Events: make(chan Event)}, nil
}
func (*Watch) Dropped() bool { return false }
func (*Watch) Close()        {}
