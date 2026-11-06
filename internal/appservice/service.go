// Package appservice coordinates source selection, observation, preview and backup.
package appservice

import (
	"context"
	"crypto/rand"
	"net"
	"net/http"
	"qqarchive/internal/catalog"
	"qqarchive/internal/discovery"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/mediacleanup"
	"qqarchive/internal/storage"
	"sync"
	"time"
)

type State struct {
	OperationResult  string                     `json:"-"`
	CheckTotal       int                        `json:"check_total"`
	CheckPass        int                        `json:"check_pass"`
	CheckStage       string                     `json:"check_stage,omitempty"`
	CheckWaiting     bool                       `json:"check_waiting"`
	UpdateRunning    bool                       `json:"update_running"`
	BackgroundStage  string                     `json:"background_stage,omitempty"`
	Stopping         bool                       `json:"stopping"`
	UpdateMode       string                     `json:"update_mode"`
	NeedsFull        bool                       `json:"needs_full"`
	Cleanup          *catalog.CleanupJob        `json:"cleanup,omitempty"`
	Restore          *mediabackup.RestoreResult `json:"restore,omitempty"`
	IndexBytes       int64                      `json:"index_bytes"`
	Phase            string                     `json:"phase"`
	Mode             string                     `json:"mode"`
	Source           *media.Source              `json:"source"`
	Sources          []catalog.SavedSource      `json:"sources"`
	Scanned          int                        `json:"scanned"`
	Bytes            int64                      `json:"bytes"`
	Report           *media.Report              `json:"report"`
	Watching         bool                       `json:"watching"`
	Coverage         string                     `json:"coverage"`
	Revision         int                        `json:"revision"`
	Error            string                     `json:"error"`
	Message          string                     `json:"message"`
	Backup           *mediabackup.Result        `json:"backup"`
	Excluded         int                        `json:"excluded"`
	ExcludedItems    []media.Issue              `json:"excluded_items"`
	Cached           bool                       `json:"cached"`
	LastFullScan     time.Time                  `json:"last_full_scan"`
	ReconcileSeconds int                        `json:"reconcile_seconds"`
	Discovery        *discovery.Result          `json:"discovery"`
}
type Media struct {
	reminderMu               sync.Mutex
	searchMu                 sync.Mutex
	directorySearch          *directorySearchJob
	directorySearchCancelled map[string]time.Time
	appearanceMu             sync.Mutex
	appearanceDraft          *appearanceImage
	discoveryInterval        time.Duration
	recycleMedia             func(context.Context, media.Item, string) error
	cleanupQQ                func() (bool, error)
	op                       sync.Mutex
	mu                       sync.RWMutex
	state                    State
	items                    []media.Item
	byID                     map[string]media.Item
	catalog                  *catalog.Catalog
	private                  string
	cancel                   context.CancelFunc
	done                     chan struct{}
	jobCancel                context.CancelFunc
	jobDone                  chan struct{}
	previews                 chan struct{}
	streamWorkers            chan struct{}
	archiveHistory           map[string][]catalog.History
	server                   *http.Server
	url                      string
	token                    string
}

func NewMedia(c *catalog.Catalog, private string) (*Media, error) {
	sources, err := c.Sources()
	if err != nil {
		return nil, err
	}
	s := &Media{catalog: c, private: private, state: State{Phase: "idle", Mode: "source", Sources: sources}, byID: map[string]media.Item{}, previews: make(chan struct{}, 2), token: rand.Text()}
	s.discoveryInterval = ReconcileInterval
	s.recycleMedia = mediacleanup.Recycle
	s.cleanupQQ = storage.QQRunning
	s.streamWorkers = make(chan struct{}, 4)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.url = "http://" + listener.Addr().String()
	s.server = &http.Server{Handler: http.HandlerFunc(s.stream), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	go s.server.Serve(listener)
	chosen, err := c.Preference("selected_source")
	if err != nil {
		s.server.Close()
		return nil, err
	}
	if chosen == "" && len(sources) > 0 {
		chosen = sources[0].ID
	}
	for _, saved := range sources {
		if saved.ID == chosen {
			if err = s.activate(savedMedia(saved), sources); err != nil {
				s.server.Close()
				return nil, err
			}
			break
		}
	}
	return s, nil
}

func (s *Media) Close() {
	s.CancelQQDirectorySearch("")
	s.op.Lock()
	defer s.op.Unlock()
	s.stopLocked()
	s.mu.RLock()
	cancel, done := s.jobCancel, s.jobDone
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.server.Close()
	s.catalog.Close()
}

func (s *Media) fail(err error) {
	s.mu.Lock()
	s.state.Error = err.Error()
	s.state.Phase = "ready"
	s.mu.Unlock()
}
func (s *Media) State() State {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	state.IndexBytes = s.catalog.DiskBytes()
	return state
}
