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

type refsKey struct{}
type videoDeliveryKey struct{}

// Refs are the pictures and clips the user sent as references for a
// generation, oldest first — "image 1" in a prompt is Images[0].
type Refs struct {
	Images []string
	Videos []string
	// Used, when set, tells the owner a generation consumed this set, so the
	// next picture the user sends starts a fresh one instead of piling on.
	Used func()
}

// MarkUsed is safe on a Refs without a Used hook.
func (r Refs) MarkUsed() {
	if r.Used != nil {
		r.Used()
	}
}

func (r Refs) Empty() bool { return len(r.Images) == 0 && len(r.Videos) == 0 }

func WithRefs(ctx context.Context, refs Refs) context.Context {
	return context.WithValue(ctx, refsKey{}, refs)
}

func RefsFrom(ctx context.Context) Refs {
	refs, _ := ctx.Value(refsKey{}).(Refs)
	return refs
}

// VideoDelivery posts a finished clip (or the reason it failed) to the chat
// that asked for it. Video renders outlive the chat request, so the tool runs
// them in the background and reports through this callback.
type VideoDelivery func(path string, err error)

func WithVideoDelivery(ctx context.Context, deliver VideoDelivery) context.Context {
	return context.WithValue(ctx, videoDeliveryKey{}, deliver)
}

// VideoDeliveryFrom returns nil outside a chat request (cron jobs, subagents).
func VideoDeliveryFrom(ctx context.Context) VideoDelivery {
	deliver, _ := ctx.Value(videoDeliveryKey{}).(VideoDelivery)
	return deliver
}
