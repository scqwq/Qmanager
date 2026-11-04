package discovery

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"strings"
)

type SearchOptions struct {
	Roots    []string
	Excluded []string
}

// Search only inspects directory entries, with one shared budget for all roots.
// Unlike explicit import, an automatic candidate needs the QQ account structure.
func Search(ctx context.Context, opts SearchOptions) (Result, error) {
	r := Result{Complete: true, Candidates: []Candidate{}}
	blocked := map[string]bool{}
	for _, p := range opts.Excluded {
		if filepath.IsAbs(p) {
			blocked[strings.ToLower(filepath.Clean(p))] = true
		}
	}
	within := func(path string, roots map[string]bool) bool {
		for p := filepath.Clean(path); ; p = filepath.Dir(p) {
			if roots[strings.ToLower(p)] {
				return true
			}
			if filepath.Dir(p) == p {
				return false
			}
		}
	}
	found, visited := map[string]bool{}, map[string]bool{}
	entries, pathBytes := 0, 0
	type node struct {
		path, root, rel string
		depth           int
	}
	for _, supplied := range opts.Roots {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if supplied == "" {
			continue
		}
		if !filepath.IsAbs(supplied) || strings.HasPrefix(supplied, `\\`) {
			r.Complete = false
			r.Skipped++
			continue
		}
		root := filepath.Clean(supplied)
		base := strings.ToLower(filepath.Base(root))
		if excluded[base] || base == "emoji-recv" || base == ".qman" || base == ".local" {
			continue
		}
		if filepath.Dir(root) == root {
			r.Complete = false
			r.Skipped++
			continue
		}
		if within(root, blocked) || within(root, found) {
			continue
		}
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			r.Complete = false
			r.Skipped++
			continue
		}
		if _, err := media.Canonical(root); err != nil {
			r.Complete = false
			r.Skipped++
			continue
		}
		if backupAncestor(root) {
			continue
		}
		queue := []node{{root, root, ".", 0}}
		for head := 0; head < len(queue); head++ {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			n := queue[head]
			key := strings.ToLower(n.path)
			if visited[key] || within(n.path, blocked) || within(n.path, found) {
				continue
			}
			if r.Directories >= MaxDirectories {
				r.Complete = false
				r.Reason = "目录数量达到发现上限"
				return r, nil
			}
			visited[key] = true
			r.Directories++
			dir, err := media.OpenRelative(n.root, n.rel)
			if err != nil {
				r.Complete = false
				r.Skipped++
				continue
			}
			if backupDirectory(n.path) {
				dir.Close()
				continue
			}
			if qqDataDirectory(n.path) {
				dir.Close()
				candidate := n.path
				for p := n.path; filepath.Dir(p) != p; p = filepath.Dir(p) {
					if strings.EqualFold(filepath.Base(p), "Tencent Files") {
						candidate = p
						break
					}
				}
				if !within(candidate, blocked) && !backupAncestor(candidate) && !found[strings.ToLower(candidate)] {
					found[strings.ToLower(candidate)] = true
					r.Candidates = append(r.Candidates, Candidate{Root: candidate, Name: name(n.path)})
					if len(r.Candidates) >= MaxSources {
						r.Complete = false
						r.Reason = "来源数量达到发现上限"
						return r, nil
					}
				}
				continue
			}
			for {
				if err = ctx.Err(); err != nil {
					dir.Close()
					return r, err
				}
				batch, readErr := dir.ReadDir(128)
				for _, entry := range batch {
					if err = ctx.Err(); err != nil {
						dir.Close()
						return r, err
					}
					entries++
					if entries > MaxEntries {
						dir.Close()
						r.Complete = false
						r.Reason = "目录条目达到发现上限"
						return r, nil
					}
					if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
						r.Complete = false
						r.Skipped++
						continue
					}
					if !entry.IsDir() || excluded[strings.ToLower(entry.Name())] || strings.EqualFold(entry.Name(), ".local") || strings.EqualFold(entry.Name(), ".qman") {
						continue
					}
					if n.depth >= MaxDepth {
						r.Complete = false
						continue
					}
					rel := filepath.Join(n.rel, entry.Name())
					pathBytes += len(rel)
					if pathBytes > 4*1024*1024 || len(queue)-head+r.Directories >= MaxDirectories {
						dir.Close()
						r.Complete = false
						r.Reason = "目录数量达到发现上限"
						return r, nil
					}
					queue = append(queue, node{filepath.Join(n.root, rel), n.root, rel, n.depth + 1})
				}
				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					r.Complete = false
					r.Skipped++
					break
				}
			}
			dir.Close()
		}
		// A successful higher-priority location needs no broader fallback search.
		if len(r.Candidates) > 0 {
			return r, ctx.Err()
		}
	}
	return r, ctx.Err()
}

func qqDataDirectory(path string) bool {
	if !strings.EqualFold(filepath.Base(path), "nt_data") || !strings.EqualFold(filepath.Base(filepath.Dir(path)), "nt_qq") {
		return false
	}
	for _, rel := range []string{"Pic", "Video", "Emoji/emoji-recv", "Ptt"} {
		f, err := media.OpenRelative(path, filepath.FromSlash(rel))
		if err != nil {
			continue
		}
		info, e := f.Stat()
		f.Close()
		if e == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func backupDirectory(path string) bool {
	info, err := os.Lstat(filepath.Join(path, "backup.json"))
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	_, err = os.Lstat(filepath.Join(path, ".qman"))
	return err == nil
}

func backupAncestor(path string) bool {
	for p := path; ; p = filepath.Dir(p) {
		if backupDirectory(p) {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}
