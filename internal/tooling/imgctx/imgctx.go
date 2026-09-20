// Package imgctx carries per-request image state through the tool loop.
//
// A Telegram handler knows which photo the user attached and wants the files a
// tool produced; the tool in between only sees a context.Context. Rather than
// widening the Tool interface for one tool, both ends meet on these context keys.
package imgctx

import (
	"context"
	"sync"
)

type currentKey struct{}
type sinkKey struct{}

// Sink collects the image files a tool produced during one request.
// The zero value is ready to use, and a nil *Sink absorbs writes so tools can
// run outside a request that installed one (cron jobs, subagents).
type Sink struct {
	mu    sync.Mutex
	paths []string
}

func (s *Sink) Add(path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
}

func (s *Sink) Paths() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// WithCurrent marks the image the request is about — the photo just attached, or
// the last one seen in this chat — so an edit can run without re-uploading it.
func WithCurrent(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, currentKey{}, path)
}

func Current(ctx context.Context) string {
	path, _ := ctx.Value(currentKey{}).(string)
	return path
}

func WithSink(ctx context.Context, sink *Sink) context.Context {
	return context.WithValue(ctx, sinkKey{}, sink)
}

// SinkFrom never returns nil-unsafe results: the nil *Sink is usable.
func SinkFrom(ctx context.Context) *Sink {
	sink, _ := ctx.Value(sinkKey{}).(*Sink)
	return sink
}
