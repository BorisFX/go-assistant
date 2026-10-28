package a6video_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/a6video"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
)

var (
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIFfake")
	mp4Bytes  = []byte("\x00\x00\x00\x18ftypmp42fake-video")
)

type submitBody struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Seconds        string `json:"seconds"`
	Size           string `json:"size"`
	InputReference string `json:"input_reference"`
	Metadata       struct {
		Ratio   string `json:"ratio"`
		Content []any  `json:"content"`
	} `json:"metadata"`
}

// fakeGateway emulates /videos: submit, a few in-progress polls, then content.
type fakeGateway struct {
	mu          sync.Mutex
	submits     []submitBody
	submitCodes []int // status per submit attempt; 200 once exhausted
	polls       int
	finalStatus string
	proxyPage   bool // 502s come as an HTML proxy page instead of a JSON error
	busyCode    bool // errors carry the "no supplier took it" code
}

func (g *fakeGateway) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /videos", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		var body submitBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode submit: %v", err)
		}
		g.submits = append(g.submits, body)
		if n := len(g.submits) - 1; n < len(g.submitCodes) && g.submitCodes[n] != 200 {
			w.WriteHeader(g.submitCodes[n])
			if g.submitCodes[n] == http.StatusBadGateway && g.proxyPage {
				w.Write([]byte(`<html>Origin Not Reachable</html>`))
				return
			}
			if g.busyCode {
				w.Write([]byte(`{"error":{"message":"busy","code":"video_submit_upstream_acceptance_unknown"}}`))
				return
			}
			w.Write([]byte(`{"error":{"message":"gateway says no"}}`))
			return
		}
		w.Write([]byte(`{"id":"task_1","status":"queued"}`))
	})
	mux.HandleFunc("GET /videos/task_1", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.polls++
		status := "in_progress"
		if g.polls >= 3 {
			status = g.finalStatus
		}
		if status == "failed" {
			w.Write([]byte(`{"id":"task_1","status":"failed","error":{"message":"content rejected"}}`))
			return
		}
		w.Write([]byte(`{"id":"task_1","status":"` + status + `"}`))
	})
	mux.HandleFunc("GET /videos/task_1/content", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(mp4Bytes)
	})
	return mux
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newClient(t *testing.T, g *fakeGateway) (*a6video.Client, string) {
	srv := httptest.NewServer(g.handler(t))
	t.Cleanup(srv.Close)
	out := t.TempDir()
	return a6video.New("key", "doubao-seedance-2.0-fast", srv.URL, out).WithPollInterval(time.Millisecond), out
}

func TestGenerateVideoSendsStartImageAsInputReference(t *testing.T) {
	g := &fakeGateway{finalStatus: "completed"}
	client, out := newClient(t, g)
	dir := t.TempDir()

	path, err := client.GenerateVideo(context.Background(), output.VideoRequest{
		Prompt:     "the cat drives the car",
		StartImage: writeFile(t, dir, "frame.jpg", jpegBytes),
		Seconds:    5,
		Ratio:      "9:16",
	})
	if err != nil {
		t.Fatalf("GenerateVideo: %v", err)
	}
	if !strings.HasPrefix(path, out) {
		t.Errorf("video saved to %q, want under %q", path, out)
	}
	if got, _ := os.ReadFile(path); string(got) != string(mp4Bytes) {
		t.Errorf("saved bytes differ from served video")
	}

	body := g.submits[0]
	if body.Size != "1280x720" || body.Seconds != "5" || body.Model != "doubao-seedance-2.0-fast" {
		t.Errorf("unexpected spec: %+v", body)
	}
	if !strings.HasPrefix(body.InputReference, "data:image/jpeg;base64,") {
		t.Errorf("input_reference = %.40q, want a jpeg data URI", body.InputReference)
	}
	// metadata.content is billed and ignored by the gateway: never send it.
	if body.Metadata.Content != nil {
		t.Errorf("metadata.content sent: %v", body.Metadata.Content)
	}
	if body.Metadata.Ratio != "" {
		t.Errorf("ratio = %q, want none when a start frame sets the shape", body.Metadata.Ratio)
	}
}

func TestGenerateVideoWithoutReferencesSendsNoContent(t *testing.T) {
	g := &fakeGateway{finalStatus: "completed"}
	client, _ := newClient(t, g)

	if _, err := client.GenerateVideo(context.Background(), output.VideoRequest{Prompt: "a cat", Seconds: 99, Ratio: "9:16"}); err != nil {
		t.Fatalf("GenerateVideo: %v", err)
	}
	body := g.submits[0]
	if body.InputReference != "" {
		t.Errorf("input_reference sent without a start image")
	}
	if body.Seconds != "15" {
		t.Errorf("seconds = %q, want clamped to 15", body.Seconds)
	}
	if body.Metadata.Ratio != "9:16" {
		t.Errorf("ratio = %q, want 9:16", body.Metadata.Ratio)
	}
}

func TestGenerateVideoRetriesGatewayErrorsButNotRejections(t *testing.T) {
	g := &fakeGateway{finalStatus: "completed", submitCodes: []int{502, 200}, proxyPage: true}
	client, _ := newClient(t, g)
	if _, err := client.GenerateVideo(context.Background(), output.VideoRequest{Prompt: "x"}); err != nil {
		t.Fatalf("proxy 502 then 200 should succeed: %v", err)
	}

	// "No supplier took it, refunded" is retried after a pause.
	g = &fakeGateway{finalStatus: "completed", submitCodes: []int{502, 200}, busyCode: true}
	client, _ = newClient(t, g)
	if _, err := client.GenerateVideo(context.Background(), output.VideoRequest{Prompt: "x"}); err != nil {
		t.Fatalf("busy supplier then success should succeed: %v", err)
	}

	g = &fakeGateway{finalStatus: "completed", submitCodes: []int{502, 502, 502}, busyCode: true}
	client, _ = newClient(t, g)
	_, err := client.GenerateVideo(context.Background(), output.VideoRequest{Prompt: "x"})
	if !errors.Is(err, a6video.ErrSupplierBusy) {
		t.Fatalf("persistent busy supplier: got %v, want ErrSupplierBusy", err)
	}

	g = &fakeGateway{finalStatus: "completed", submitCodes: []int{400, 200}}
	client, _ = newClient(t, g)
	_, err = client.GenerateVideo(context.Background(), output.VideoRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "gateway says no") {
		t.Fatalf("400 should fail with the gateway message, got %v", err)
	}
	if len(g.submits) != 1 {
		t.Errorf("400 was retried: %d submits", len(g.submits))
	}
}

func TestGenerateVideoReportsFailedTask(t *testing.T) {
	g := &fakeGateway{finalStatus: "failed"}
	client, _ := newClient(t, g)
	_, err := client.GenerateVideo(context.Background(), output.VideoRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "content rejected") {
		t.Fatalf("want task failure with reason, got %v", err)
	}
}

func TestClampSeconds(t *testing.T) {
	for in, want := range map[int]int{0: 5, -3: 5, 2: 4, 8: 8, 40: 15} {
		if got := a6video.ClampSeconds(in); got != want {
			t.Errorf("ClampSeconds(%d) = %d, want %d", in, got, want)
		}
	}
}
