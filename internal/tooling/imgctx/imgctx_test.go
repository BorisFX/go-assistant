package imgctx_test

import (
	"context"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

func TestCurrentReturnsStoredPath(t *testing.T) {
	ctx := imgctx.WithCurrent(context.Background(), "/files/photo.jpg")

	if got := imgctx.Current(ctx); got != "/files/photo.jpg" {
		t.Fatalf("Current() = %q, want /files/photo.jpg", got)
	}
}

func TestCurrentIsEmptyWhenUnset(t *testing.T) {
	if got := imgctx.Current(context.Background()); got != "" {
		t.Fatalf("Current() = %q, want empty", got)
	}
}

func TestSinkCollectsPathsAddedThroughContext(t *testing.T) {
	sink := &imgctx.Sink{}
	ctx := imgctx.WithSink(context.Background(), sink)

	imgctx.SinkFrom(ctx).Add("/files/out1.png")
	imgctx.SinkFrom(ctx).Add("/files/out2.png")

	paths := sink.Paths()
	if len(paths) != 2 || paths[0] != "/files/out1.png" || paths[1] != "/files/out2.png" {
		t.Fatalf("Paths() = %v, want both outputs in order", paths)
	}
}

// A tool must be able to run outside a Telegram request, where nobody installed a sink.
func TestSinkFromWithoutSinkIsSafeToUse(t *testing.T) {
	imgctx.SinkFrom(context.Background()).Add("/files/out.png")
}
