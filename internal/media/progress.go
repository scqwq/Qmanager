package media

import (
	"context"
	"io"
	"time"
)

type TaskProgress struct {
	Stage string `json:"stage"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Part  int    `json:"part,omitempty"`
	Parts int    `json:"parts,omitempty"`
}

type progressKey struct{}

func WithProgress(ctx context.Context, report func(TaskProgress)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

type Progress struct {
	report func(TaskProgress)
	state  TaskProgress
	last   time.Time
}

func StartProgress(ctx context.Context, stage string, total int64) *Progress {
	report, _ := ctx.Value(progressKey{}).(func(TaskProgress))
	p := &Progress{report: report, state: TaskProgress{Stage: stage, Total: total}}
	p.publish()
	return p
}

func (p *Progress) publish() {
	if p.report != nil {
		p.last = time.Now()
		p.report(p.state)
	}
}

func (p *Progress) Set(done int64) {
	p.state.Done = done
	if p.report != nil && (done >= p.state.Total || time.Since(p.last) >= 100*time.Millisecond) {
		p.publish()
	}
}

func (p *Progress) Part(n, total int) {
	p.state.Part, p.state.Parts = n, total
	p.publish()
}

func (p *Progress) Write(b []byte) (int, error) {
	p.Set(p.state.Done + int64(len(b)))
	return len(b), nil
}

func (p *Progress) Writer(w io.Writer) io.Writer {
	if p.report == nil {
		return w
	}
	return io.MultiWriter(w, p)
}
