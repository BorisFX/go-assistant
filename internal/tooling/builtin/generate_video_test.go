package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

type fakeVideoGen struct {
	got     chan output.VideoRequest
	release chan struct{}
	err     error
}

func newFakeVideoGen() *fakeVideoGen {
	return &fakeVideoGen{got: make(chan output.VideoRequest, 4), release: make(chan struct{})}
}

func (f *fakeVideoGen) GenerateVideo(ctx context.Context, req output.VideoRequest) (string, error) {
	f.got <- req
	<-f.release
	if f.err != nil {
		return "", f.err
	}
	return "/files/clip.mp4", nil
}

type fakeComposer struct {
	paths  []string
	prompt string
	size   string
	err    error
}

func (f *fakeComposer) Compose(ctx context.Context, paths []string, prompt, size string) (string, error) {
	f.paths, f.prompt, f.size = paths, prompt, size
	if f.err != nil {
		return "", f.err
	}
	return "/files/frame.png", nil
}

type delivered struct {
	path string
	err  error
}

func videoCtx(refs imgctx.Refs) (context.Context, chan delivered, *imgctx.Sink) {
	out := make(chan delivered, 4)
	sink := &imgctx.Sink{}
	ctx := imgctx.WithRefs(context.Background(), refs)
	ctx = imgctx.WithSink(ctx, sink)
	ctx = imgctx.WithVideoDelivery(ctx, func(path string, err error) { out <- delivered{path, err} })
	return ctx, out, sink
}

func waitDelivery(t *testing.T, ch chan delivered) delivered {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("video was never delivered")
		return delivered{}
	}
}

func TestGenerateVideoBlendsSeveralReferencesIntoOneFrame(t *testing.T) {
	gen := newFakeVideoGen()
	composer := &fakeComposer{}
	tool := builtin.NewGenerateVideo(gen, composer, 0.046)
	used := false
	ctx, out, sink := videoCtx(imgctx.Refs{Images: []string{"/cat.jpg", "/car.jpg"}, Used: func() { used = true }})

	res, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"the cat from [Image 1] drives the car from [Image 2]","ratio":"9:16"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(composer.paths) != 2 || composer.paths[0] != "/cat.jpg" || composer.size != "1024x1536" {
		t.Errorf("compose got %v size %q", composer.paths, composer.size)
	}
	if !strings.Contains(composer.prompt, "[Image 1], [Image 2]") || !strings.Contains(composer.prompt, "drives the car") {
		t.Errorf("compose prompt = %q", composer.prompt)
	}
	if paths := sink.Paths(); len(paths) != 1 || paths[0] != "/files/frame.png" {
		t.Errorf("sink = %v, want the opening frame shown to the user", paths)
	}
	if !used {
		t.Error("reference set was not marked used")
	}
	if !strings.Contains(string(res), `"status":"rendering"`) || !strings.Contains(string(res), `"estimated_cost_usd":0.23`) {
		t.Errorf("result = %s", res)
	}

	req := <-gen.got
	if req.StartImage != "/files/frame.png" || req.Seconds != 5 || !req.Audio {
		t.Errorf("request = %+v", req)
	}
	close(gen.release)
	if d := waitDelivery(t, out); d.path != "/files/clip.mp4" || d.err != nil {
		t.Errorf("delivered %+v", d)
	}
}

func TestGenerateVideoUsesASingleReferenceDirectly(t *testing.T) {
	gen := newFakeVideoGen()
	close(gen.release)
	composer := &fakeComposer{}
	tool := builtin.NewGenerateVideo(gen, composer, 0)
	ctx, out, sink := videoCtx(imgctx.Refs{Images: []string{"/still.jpg"}})

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"the man from [Image 1] as a clown"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if composer.paths != nil {
		t.Error("a single reference was sent through the composer")
	}
	if len(sink.Paths()) != 0 {
		t.Error("nothing was drawn, yet a frame was queued for the user")
	}
	if req := <-gen.got; req.StartImage != "/still.jpg" {
		t.Errorf("start image = %q", req.StartImage)
	}
	waitDelivery(t, out)
}

func TestGenerateVideoCanIgnoreReferences(t *testing.T) {
	gen := newFakeVideoGen()
	close(gen.release)
	tool := builtin.NewGenerateVideo(gen, &fakeComposer{}, 0)
	ctx, out, _ := videoCtx(imgctx.Refs{Images: []string{"/a.jpg"}})

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"sunset","use_references":false,"seconds":30}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	req := <-gen.got
	if req.StartImage != "" || req.Seconds != 15 {
		t.Errorf("request = %+v, want text-only clamped to 15 s", req)
	}
	waitDelivery(t, out)
}

func TestGenerateVideoFallsBackToCurrentPicture(t *testing.T) {
	gen := newFakeVideoGen()
	close(gen.release)
	tool := builtin.NewGenerateVideo(gen, &fakeComposer{}, 0)
	ctx, out, _ := videoCtx(imgctx.Refs{})
	ctx = imgctx.WithCurrent(ctx, "/files/drawn.png")

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"animate it"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if req := <-gen.got; req.StartImage != "/files/drawn.png" {
		t.Errorf("start image = %q, want the current picture", req.StartImage)
	}
	waitDelivery(t, out)
}

func TestGenerateVideoStopsWhenFrameCannotBeDrawn(t *testing.T) {
	gen := newFakeVideoGen()
	tool := builtin.NewGenerateVideo(gen, &fakeComposer{err: errors.New("refused")}, 0)
	ctx, _, _ := videoCtx(imgctx.Refs{Images: []string{"/a.jpg", "/b.jpg"}})

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Fatal("render started without its opening frame")
	}
	// The slot must be free again.
	close(gen.release)
	for range 2 {
		ctx, out, _ := videoCtx(imgctx.Refs{})
		if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"x"}`)); err != nil {
			t.Fatalf("slot leaked: %v", err)
		}
		<-gen.got
		waitDelivery(t, out)
	}
}

func TestGenerateVideoReportsFailureThroughDelivery(t *testing.T) {
	gen := newFakeVideoGen()
	gen.err = errors.New("content rejected")
	close(gen.release)
	tool := builtin.NewGenerateVideo(gen, nil, 0)
	ctx, out, _ := videoCtx(imgctx.Refs{})

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"x"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	<-gen.got
	if d := waitDelivery(t, out); d.err == nil {
		t.Error("failure was not delivered")
	}
}

func TestGenerateVideoLimitsConcurrentRenders(t *testing.T) {
	gen := newFakeVideoGen()
	tool := builtin.NewGenerateVideo(gen, nil, 0)
	ctx, out, _ := videoCtx(imgctx.Refs{})

	for range 2 {
		if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"x"}`)); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Error("third concurrent render was accepted")
	}
	close(gen.release)
	waitDelivery(t, out)
	waitDelivery(t, out)
}

func TestGenerateVideoNeedsAChat(t *testing.T) {
	tool := builtin.NewGenerateVideo(newFakeVideoGen(), nil, 0)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"x"}`)); err == nil {
		t.Error("render started with nowhere to deliver it")
	}
}

func TestGenerateVideoRefusesToCiteMissingPictures(t *testing.T) {
	gen := newFakeVideoGen()
	tool := builtin.NewGenerateVideo(gen, nil, 0)
	ctx, _, _ := videoCtx(imgctx.Refs{})

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"the man from [Image 1] cries"}`)); err == nil {
		t.Fatal("rendered a clip citing a picture that was never sent")
	}
	select {
	case <-gen.got:
		t.Error("generator was called")
	default:
	}
}
