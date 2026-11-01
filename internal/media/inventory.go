package media

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ScanOptions struct {
	NewOnly  bool
	Known    map[string]bool
	Previous map[string]Item
	Force    map[string]bool
	Targets  []string
}

func Scan(ctx context.Context, source Source, progress func(int, int64)) Report {
	return ScanWith(ctx, source, ScanOptions{}, progress)
}
func Covers(prefixes []string, path string) bool {
	for _, prefix := range prefixes {
		if prefix == "." || path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// A missing leaf proves absence only after all existing ancestors have been
// checked; an internal junction must not turn a failed lookup into evidence.
func checkedAbsence(root, relative string) bool {
	for p := filepath.Dir(relative); ; p = filepath.Dir(p) {
		if _, err := checkedPath(root, p); err == nil {
			return true
		} else if !os.IsNotExist(err) {
			return false
		}
		if p == "." || filepath.Dir(p) == p {
			return false
		}
	}
}
func Bind(item Item, source Source) Item {
	item.Root = source.Resolved
	item.Path = filepath.FromSlash(item.RelativePath)
	return item
}
func forced(paths map[string]bool, path string) bool {
	// A parent-directory notification is not evidence that every child's bytes
	// changed. Those children still get identity/size/mtime checks.
	return paths[path] || paths["."]
}

// ScanWith always checks current file identity/metadata. Format bytes are reused
// only for unchanged instances; native modification hints force re-detection.
func ScanWith(ctx context.Context, source Source, opts ScanOptions, progress func(int, int64)) Report {
	r := Report{StartedAt: time.Now().UTC(), Complete: true, Partial: opts.Targets != nil || opts.NewOnly, Quick: opts.NewOnly, NamesEnumerated: opts.Targets == nil, Items: []Item{}, Issues: []Issue{}, Buckets: []Bucket{}}
	known := func(path string) bool {
		if opts.Known[path] {
			return true
		}
		_, ok := opts.Previous[path]
		return ok
	}
	if opts.NewOnly {
		r.UpdateKind = "fast"
	} else {
		r.UpdateKind = "full"
	}
	if opts.Targets != nil {
		r.UpdateKind = "targeted"
	}
	issue := func(path, code string) {
		r.Complete = false
		r.IssueCount++
		if len(r.Issues) < MaxIssues {
			r.Issues = append(r.Issues, Issue{Path: filepath.ToSlash(path), Code: code})
		}
	}
	directories, pathBytes := 0, 0
	seen := map[string]bool{}
	inspect := func(scope Scope, rel string, info os.FileInfo) error {
		if r.Files >= MaxFiles {
			return errors.New("达到文件上限")
		}
		relative := filepath.ToSlash(filepath.Join(scope.Prefix, rel))
		if seen[relative] {
			return nil
		}
		seen[relative] = true
		month, role := monthAndRole(rel, scope.Kind)
		item := Item{ID: rand.Text(), RelativePath: relative, Kind: scope.Kind, SourceMonth: month, Role: role, Bytes: info.Size(), ModifiedAt: info.ModTime().UTC(), CreatedAt: createdAt(info), Format: "unknown", Status: "unreadable", Root: scope.Path, Path: rel, InfoIdentity: info}
		f, e := openRead(scope.Path, rel)
		if e == nil {
			before, se := f.Stat()
			item.Identity = fileIdentity(f)
			if se == nil && os.SameFile(info, before) && info.Size() == before.Size() && info.ModTime().Equal(before.ModTime()) {
				old, ok := opts.Previous[relative]
				if ok && !forced(opts.Force, relative) && item.Identity != "" && old.Identity == item.Identity && old.Bytes == item.Bytes && old.ModifiedAt.Equal(item.ModifiedAt) && old.Status != "unreadable" && old.Status != "changed" {
					item.Format = old.Format
					item.Status = old.Status
					item.Preview = old.Preview
					r.Reused++
				} else {
					item.Format, item.Status, item.Preview = detect(f, item.Bytes)
					if item.Bytes > 0 {
						r.HeaderReads++
					}
				}
				after, ae := f.Stat()
				current, ce := os.Lstat(filepath.Join(scope.Path, rel))
				if ae != nil || ce != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
					item.Status = "changed"
					item.Preview = false
				}
			} else {
				item.Status = "changed"
			}
			f.Close()
		}
		if item.Status == "unreadable" && item.Identity == "" {
			if old, ok := opts.Previous[relative]; ok && old.Bytes == item.Bytes && old.ModifiedAt.Equal(item.ModifiedAt) {
				item.Identity = old.Identity
			}
		}
		if item.Status == "unreadable" || item.Status == "changed" {
			issue(relative, item.Status)
		}
		r.Items = append(r.Items, item)
		r.Files++
		r.Bytes += item.Bytes
		if progress != nil && r.Files%100 == 0 {
			progress(r.Files, r.Bytes)
		}
		return nil
	}
	var walk func(Scope, string, int) error
	walk = func(scope Scope, relative string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 16 || directories >= 50000 {
			issue(relative, "directory_limit")
			return errors.New("达到目录上限")
		}
		directories++
		dir, err := openRead(scope.Path, relative)
		if os.IsNotExist(err) && relative == "." {
			return nil
		}
		if err != nil {
			issue(filepath.Join(scope.Prefix, relative), "directory_unreadable_or_link")
			return nil
		}
		defer dir.Close()
		for {
			entries, readErr := dir.ReadDir(128)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				rel := filepath.Join(relative, entry.Name())
				r.EntriesChecked++
				if opts.NewOnly && progress != nil && r.EntriesChecked%500 == 0 {
					progress(r.EntriesChecked, r.Bytes)
				}
				pathBytes += len(rel)
				if pathBytes > 64*1024*1024 {
					issue(rel, "metadata_limit")
					return errors.New("达到路径元数据上限")
				}
				logical := filepath.ToSlash(filepath.Join(scope.Prefix, rel))
				if opts.NewOnly && entry.Type().IsRegular() && known(logical) && !forced(opts.Force, logical) {
					r.KnownSkipped++
					if r.KnownSkipped+r.Files > MaxFiles {
						issue(logical, "file_limit")
						return errors.New("达到文件上限")
					}
					continue
				}
				path := filepath.Join(scope.Path, rel)
				info, e := os.Lstat(path)
				if e != nil {
					issue(filepath.Join(scope.Prefix, rel), "stat_failed")
					continue
				}
				if isLink(path, info) {
					issue(filepath.Join(scope.Prefix, rel), "link_skipped")
					continue
				}
				if info.IsDir() {
					if e = walk(scope, rel, depth+1); e != nil {
						return e
					}
					continue
				}
				if !info.Mode().IsRegular() {
					issue(rel, "non_regular_skipped")
					continue
				}
				if e = inspect(scope, rel, info); e != nil {
					issue(rel, "file_limit")
					return e
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				issue(relative, "directory_read_failed")
				return nil
			}
		}
	}
	if opts.Targets == nil {
		for _, scope := range source.Scopes {
			if e := walk(scope, ".", 0); e != nil {
				r.Complete = false
				r.Cancelled = ctx.Err() != nil
				break
			}
		}
	} else {
		targets := append([]string(nil), opts.Targets...)
		sort.Strings(targets)
		visited := []string{}
		for _, target := range targets {
			if ctx.Err() != nil {
				r.Cancelled = true
				r.Complete = false
				break
			}
			target = filepath.ToSlash(filepath.Clean(filepath.FromSlash(target)))
			if !filepath.IsLocal(filepath.FromSlash(target)) || strings.Contains(target, ":") {
				issue(target, "invalid_event_path")
				continue
			}
			if Covers(visited, target) {
				continue
			}
			visited = append(visited, target)
			matched := false
			for _, scope := range source.Scopes {
				rel := target
				if scope.Prefix != "" {
					if target == scope.Prefix {
						rel = "."
					} else if strings.HasPrefix(target, scope.Prefix+"/") {
						rel = strings.TrimPrefix(target, scope.Prefix+"/")
					} else {
						continue
					}
				}
				matched = true
				p, e := checkedPath(scope.Path, filepath.FromSlash(rel))
				if os.IsNotExist(e) {
					if checkedAbsence(scope.Path, filepath.FromSlash(rel)) {
						r.Covered = append(r.Covered, target)
					} else {
						issue(target, "directory_unreadable_or_link")
					}
					continue
				}
				if e != nil {
					issue(target, "directory_unreadable_or_link")
					continue
				}
				info, e := os.Lstat(p)
				if e != nil {
					issue(target, "stat_failed")
					continue
				}
				before := r.IssueCount
				if info.IsDir() {
					e = walk(scope, filepath.FromSlash(rel), 0)
				} else if info.Mode().IsRegular() {
					r.EntriesChecked++
					e = inspect(scope, filepath.FromSlash(rel), info)
				} else {
					issue(target, "non_regular_skipped")
				}
				if e != nil {
					issue(target, "incomplete_target")
				}
				if r.IssueCount == before {
					if !opts.NewOnly || !info.IsDir() {
						r.Covered = append(r.Covered, target)
					}
				}
			}
			if !matched {
				issue(target, "invalid_event_path")
			}
		}
	}
	sort.Slice(r.Items, func(i, j int) bool {
		if r.Items[i].SourceMonth != r.Items[j].SourceMonth {
			return r.Items[i].SourceMonth > r.Items[j].SourceMonth
		}
		return r.Items[i].RelativePath < r.Items[j].RelativePath
	})
	buckets := map[string]*Bucket{}
	for _, item := range r.Items {
		key := item.Kind + "/" + item.SourceMonth + "/" + item.Role
		if buckets[key] == nil {
			buckets[key] = &Bucket{Kind: item.Kind, Month: item.SourceMonth, Role: item.Role}
		}
		buckets[key].Files++
		buckets[key].Bytes += item.Bytes
	}
	keys := []string{}
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		r.Buckets = append(r.Buckets, *buckets[key])
	}
	r.FinishedAt = time.Now().UTC()
	r.Inspected = r.Files
	if progress != nil {
		if opts.NewOnly {
			progress(r.EntriesChecked, r.Bytes)
		} else {
			progress(r.Files, r.Bytes)
		}
	}
	return r
}
