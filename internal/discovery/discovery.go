// Package discovery finds media roots inside one explicitly selected directory.
// Discovery enumerates directory names only, never opens media or QQ databases.
package discovery

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"sort"
	"strings"
)

const MaxDirectories = 4096
const MaxEntries = 32768
const MaxDepth = 6
const MaxSources = 32

type Candidate struct {
	Root string `json:"root"`
	Name string `json:"name"`
}
type Result struct {
	Root        string      `json:"root"`
	Linked      bool        `json:"linked"`
	Candidates  []Candidate `json:"candidates"`
	Directories int         `json:"directories"`
	Skipped     int         `json:"skipped"`
	Complete    bool        `json:"complete"`
	Reason      string      `json:"reason"`
}

var excluded = map[string]bool{"msf": true, "mmkv": true, "log": true, "log-cache": true, "unitedconfig": true, "avatar": true, "qzone": true, "file": true, "pic": true, "video": true, "ptt": true, "emoji": true, "node_modules": true, ".git": true}

func name(root string) string {
	p := filepath.Dir(root)
	if strings.EqualFold(filepath.Base(root), "nt_data") && strings.EqualFold(filepath.Base(p), "nt_qq") {
		return filepath.Base(filepath.Dir(p))
	}
	return filepath.Base(root)
}
func Find(ctx context.Context, selected string) (Result, error) {
	r := Result{Complete: true, Candidates: []Candidate{}}
	absolute, err := filepath.Abs(selected)
	if err != nil {
		return r, err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return r, errors.New("无法读取所选目录")
	}
	r.Root = root
	if filepath.Dir(root) == root {
		return r, errors.New("请选择具体文件夹，不能从整个磁盘发现账号")
	}
	r.Linked = !strings.EqualFold(absolute, root)
	if source, e := media.Resolve(root); e == nil {
		r.Candidates = append(r.Candidates, Candidate{source.Resolved, name(source.Resolved)})
		return r, nil
	}
	type node struct {
		rel   string
		depth int
	}
	queue := []node{{".", 0}}
	entries := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		n := queue[0]
		queue = queue[1:]
		if r.Directories >= MaxDirectories {
			r.Complete = false
			r.Reason = "目录数量达到发现上限"
			break
		}
		r.Directories++
		path := filepath.Join(root, n.rel)
		if n.rel != "." {
			if source, e := media.Resolve(path); e == nil {
				r.Candidates = append(r.Candidates, Candidate{source.Resolved, name(source.Resolved)})
				if len(r.Candidates) >= MaxSources {
					r.Complete = false
					r.Reason = "已达到 32 个来源上限"
					break
				}
				continue
			}
		}
		dir, e := media.OpenRelative(root, n.rel)
		if e != nil {
			r.Skipped++
			r.Complete = false
			continue
		}
		stop := false
		for {
			batch, e := dir.ReadDir(128)
			for _, entry := range batch {
				entries++
				if entries > MaxEntries {
					r.Complete = false
					r.Reason = "目录条目达到发现上限"
					stop = true
					break
				}
				if ctx.Err() != nil {
					dir.Close()
					return r, ctx.Err()
				}
				if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
					r.Skipped++
					r.Complete = false
					continue
				}
				if !entry.IsDir() {
					if entry.Type()&os.ModeSymlink != 0 {
						r.Skipped++
					}
					continue
				}
				if excluded[strings.ToLower(entry.Name())] {
					continue
				}
				rel := filepath.Join(n.rel, entry.Name())
				if n.depth >= MaxDepth {
					r.Complete = false
					r.Skipped++
					continue
				}
				if len(queue)+r.Directories >= MaxDirectories {
					r.Complete = false
					r.Reason = "目录数量达到发现上限"
					stop = true
					break
				}
				// The handle-checked open rejects internal junctions before Resolve may inspect them.
				check, ce := media.OpenRelative(root, rel)
				if ce != nil {
					r.Skipped++
					r.Complete = false
					continue
				}
				check.Close()
				queue = append(queue, node{rel, n.depth + 1})
			}
			if stop || e == io.EOF {
				break
			}
			if e != nil {
				r.Skipped++
				r.Complete = false
				break
			}
		}
		dir.Close()
		if stop {
			break
		}
	}
	sort.Slice(r.Candidates, func(i, j int) bool { return r.Candidates[i].Root < r.Candidates[j].Root })
	if len(r.Candidates) == 0 {
		if !r.Complete {
			return r, errors.New("发现范围存在缺口，未找到媒体根；可直接选择账号的 nt_data")
		}
		return r, errors.New("未找到媒体来源，请选择 Tencent Files、账号目录或 nt_data")
	}
	return r, nil
}
