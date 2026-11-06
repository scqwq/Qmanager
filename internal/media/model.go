// Package media inventories explicitly selected local media without changing it.
package media

import "time"

const MaxFiles = 200000
const MaxIssues = 1000

type Scope struct {
	Path   string `json:"-"`
	Kind   string `json:"kind"`
	Prefix string `json:"-"`
}
type Source struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Selected string  `json:"selected"`
	Resolved string  `json:"resolved"`
	Linked   bool    `json:"linked"`
	Scopes   []Scope `json:"scopes"`
}

// CoverID is derived for display only; it is never a file identity or backup entry.
type Item struct {
	InstanceID         string     `json:"instance_id"`
	Identity           string     `json:"identity"`
	FirstSeenAt        time.Time  `json:"first_seen_at"`
	LastSeenAt         time.Time  `json:"last_seen_at"`
	LastChangedAt      time.Time  `json:"last_changed_observed_at"`
	StableObservedAt   *time.Time `json:"stable_observed_at"`
	Method             string     `json:"observation_method"`
	Confidence         string     `json:"confidence"`
	Coverage           string     `json:"coverage"`
	Stability          string     `json:"stability"`
	ID                 string     `json:"id"`
	RelativePath       string     `json:"relative_path"`
	Kind               string     `json:"kind"`
	SourceMonth        string     `json:"source_month"`
	Role               string     `json:"role"`
	Format             string     `json:"format"`
	Status             string     `json:"status"`
	Bytes              int64      `json:"bytes"`
	ModifiedAt         time.Time  `json:"fs_modified_at"`
	CreatedAt          *time.Time `json:"fs_created_at"`
	Preview            bool       `json:"preview"`
	CoverID            string     `json:"cover_id,omitempty"`
	Path               string     `json:"-"`
	Root               string     `json:"-"`
	InfoIdentity       any        `json:"-"`
	PhysicalModifiedAt *time.Time `json:"-"`
	PhysicalIdentity   string     `json:"-"`
}
type Issue struct {
	Path string `json:"path"`
	Code string `json:"code"`
}
type Bucket struct {
	Kind  string `json:"kind"`
	Month string `json:"month"`
	Role  string `json:"role"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}
type Report struct {
	UpdateKind      string    `json:"update_kind,omitempty"`
	Quick           bool      `json:"quick,omitempty"`
	NamesEnumerated bool      `json:"-"`
	EntriesChecked  int       `json:"entries_checked"`
	KnownSkipped    int       `json:"known_skipped"`
	Inspected       int       `json:"inspected"`
	RemovedPaths    []string  `json:"-"`
	Partial         bool      `json:"partial"`
	Covered         []string  `json:"-"`
	HeaderReads     int       `json:"header_reads"`
	Reused          int       `json:"reused"`
	IndexedUpdates  int       `json:"indexed_updates"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	Complete        bool      `json:"complete"`
	Cancelled       bool      `json:"cancelled"`
	Files           int       `json:"files"`
	Bytes           int64     `json:"bytes"`
	Issues          []Issue   `json:"issues"`
	IssueCount      int       `json:"issue_count"`
	Buckets         []Bucket  `json:"buckets"`
	Items           []Item    `json:"-"`
}
type Filter struct {
	// Display-only restriction. Never accepted from JSON or inherited by backups.
	HideVideoCovers bool   `json:"-"`
	SourceMonth     string `json:"source_month,omitempty"`
	MonthFrom       string `json:"month_from,omitempty"`
	MonthTo         string `json:"month_to,omitempty"`
	IncludeBaseline bool   `json:"include_baseline"`
	View            string `json:"view"`
	From            string `json:"from"`
	To              string `json:"to"`
	Kind            string `json:"kind"`
	Month           string `json:"month"`
	Role            string `json:"role"`
	Status          string `json:"status"`
	MinBytes        int64  `json:"min_bytes"`
	MaxBytes        int64  `json:"max_bytes"`
	Offset          int    `json:"offset"`
	Limit           int    `json:"limit"`
}
type Page struct {
	BaselineFiles int    `json:"baseline_files"`
	Items         []Item `json:"items"`
	Total         int    `json:"total"`
	Bytes         int64  `json:"bytes"`
}

func Paginate(items []Item, f Filter) Page {
	if f.Limit < 1 || f.Limit > 100 {
		f.Limit = 60
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	p := Page{Items: []Item{}}
	for _, item := range items {
		if f.View == "observed" && item.Method == "baseline" {
			all := f
			all.IncludeBaseline = true
			if Matches(item, all) {
				p.BaselineFiles++
			}
		}
		if !Matches(item, f) {
			continue
		}
		if p.Total >= f.Offset && len(p.Items) < f.Limit {
			p.Items = append(p.Items, item)
		}
		p.Total++
		p.Bytes += item.Bytes
	}
	return p
}

func Matches(item Item, f Filter) bool {
	if f.HideVideoCovers && !VisibleRole(item.Kind, item.Role, f.Role) {
		return false
	}
	if !f.MatchesSourceMonth(item.SourceMonth) {
		return false
	}
	month := item.SourceMonth
	if f.View == "observed" {
		if (item.Method == "baseline" && !f.IncludeBaseline) || item.FirstSeenAt.IsZero() {
			return false
		}
		month = item.FirstSeenAt.Local().Format("2006-01")
	}
	if f.Month == "__unknown__" {
		if month != "" {
			return false
		}
	} else if f.Month != "" && month != f.Month {
		return false
	}
	if (f.MonthFrom != "" || f.MonthTo != "") && (month == "" || f.MonthFrom != "" && month < f.MonthFrom || f.MonthTo != "" && month > f.MonthTo) {
		return false
	}
	start, end, err := f.RecordRange()
	if err != nil || (!start.IsZero() && item.FirstSeenAt.Before(start)) || (!end.IsZero() && !item.FirstSeenAt.Before(end)) {
		return false
	}
	return !(f.Kind != "" && item.Kind != f.Kind || f.Role != "" && item.Role != f.Role || f.Status != "" && item.Status != f.Status || item.Bytes < f.MinBytes || f.MaxBytes > 0 && item.Bytes > f.MaxBytes)
}
