package a6image_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/a6image"
)

var pngBytes = []byte("\x89PNG\r\n\x1a\nfake-image-bytes")

type capturedRequest struct {
	path  string
	model string
	// prompt and image are only filled for multipart (edit) requests.
	prompt string
	image  []byte
}

// newServer answers image requests with the given bodies in order and records what it got.
func newServer(t *testing.T, bodies ...func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *[]capturedRequest) {
	t.Helper()
	var seen []capturedRequest
	var calls int

	mux := http.NewServeMux()
	handle := func(w http.ResponseWriter, r *http.Request) {
		rec := capturedRequest{path: r.URL.Path}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			rec.model = r.FormValue("model")
			rec.prompt = r.FormValue("prompt")
			file, _, err := r.FormFile("image")
			if err != nil {
				t.Errorf("form file: %v", err)
			} else {
				rec.image, _ = io.ReadAll(file)
				file.Close()
			}
		} else {
			var body struct {
				Model  string `json:"model"`
				Prompt string `json:"prompt"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			rec.model = body.Model
			rec.prompt = body.Prompt
		}
		seen = append(seen, rec)

		if calls < len(bodies) {
			bodies[calls](w, r)
		}
		calls++
	}
	mux.HandleFunc("/v1/images/generations", handle)
	mux.HandleFunc("/v1/images/edits", handle)
	mux.HandleFunc("/hosted.png", func(w http.ResponseWriter, r *http.Request) { w.Write(pngBytes) })

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &seen
}

func respondWithURL(srv func() string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"url":%q}]}`, srv()+"/hosted.png")
	}
}

func respondWithBase64(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(pngBytes))
}

func respondRefusal(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	io.WriteString(w, `{"error":{"message":"content policy violation"}}`)
}

func readResult(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read produced image: %v", err)
	}
	return data
}

func TestGenerateSavesImageFromURLResponse(t *testing.T) {
	var base string
	srv, _ := newServer(t, respondWithURL(func() string { return base }))
	base = srv.URL
	dir := t.TempDir()
	client := a6image.New("key", "gpt-image-2.5", "", srv.URL+"/v1", dir)

	path, err := client.Generate(context.Background(), "красный кот")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if filepath.Dir(path) != dir {
		t.Errorf("saved to %s, want inside %s", path, dir)
	}
	if got := readResult(t, path); string(got) != string(pngBytes) {
		t.Errorf("saved %q, want the downloaded image bytes", got)
	}
}

func TestGenerateSavesImageFromBase64Response(t *testing.T) {
	srv, _ := newServer(t, respondWithBase64)
	client := a6image.New("key", "gpt-image-2.5", "", srv.URL+"/v1", t.TempDir())

	path, err := client.Generate(context.Background(), "красный кот")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if got := readResult(t, path); string(got) != string(pngBytes) {
		t.Errorf("saved %q, want the decoded image bytes", got)
	}
}

func TestGenerateRetriesWithFallbackModelWhenPrimaryRefuses(t *testing.T) {
	srv, seen := newServer(t, respondRefusal, respondWithBase64)
	client := a6image.New("key", "gpt-image-2.5", "nano-banana-pro", srv.URL+"/v1", t.TempDir())

	path, err := client.Generate(context.Background(), "добавь шляпу")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(*seen) != 2 {
		t.Fatalf("made %d requests, want 2 (primary then fallback)", len(*seen))
	}
	if (*seen)[0].model != "gpt-image-2.5" || (*seen)[1].model != "nano-banana-pro" {
		t.Errorf("models tried = %q then %q, want primary then fallback", (*seen)[0].model, (*seen)[1].model)
	}
	if got := readResult(t, path); string(got) != string(pngBytes) {
		t.Errorf("saved %q, want the fallback model's image", got)
	}
}

func TestGenerateFailsWhenEveryModelRefuses(t *testing.T) {
	srv, _ := newServer(t, respondRefusal, respondRefusal)
	client := a6image.New("key", "gpt-image-2.5", "nano-banana-pro", srv.URL+"/v1", t.TempDir())

	_, err := client.Generate(context.Background(), "добавь шляпу")
	if err == nil {
		t.Fatal("Generate succeeded, want an error naming the refusal")
	}
	if !strings.Contains(err.Error(), "content policy violation") {
		t.Errorf("error = %v, want the provider's message preserved", err)
	}
}

func TestEditUploadsSourceImageAndPrompt(t *testing.T) {
	srv, seen := newServer(t, respondWithBase64)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jpg")
	if err := os.WriteFile(source, []byte("original-photo"), 0644); err != nil {
		t.Fatal(err)
	}
	client := a6image.New("key", "gpt-image-2.5", "", srv.URL+"/v1", dir)

	path, err := client.Edit(context.Background(), source, "добавь пиратскую шляпу")
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	req := (*seen)[0]
	if req.path != "/v1/images/edits" {
		t.Errorf("posted to %s, want /v1/images/edits", req.path)
	}
	if string(req.image) != "original-photo" {
		t.Errorf("uploaded %q, want the source photo bytes", req.image)
	}
	if req.prompt != "добавь пиратскую шляпу" {
		t.Errorf("prompt = %q, want the edit instruction", req.prompt)
	}
	if path == source {
		t.Error("Edit overwrote the source image, want a new file")
	}
}

func TestEditReportsMissingSourceFile(t *testing.T) {
	srv, _ := newServer(t, respondWithBase64)
	client := a6image.New("key", "gpt-image-2.5", "", srv.URL+"/v1", t.TempDir())

	if _, err := client.Edit(context.Background(), "/nope/missing.jpg", "добавь шляпу"); err == nil {
		t.Fatal("Edit succeeded on a missing file, want an error")
	}
}

func respondGatewayError(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadGateway)
	io.WriteString(w, "<html>502 Origin Not Reachable</html>")
}

// The gateway 502s on roughly every other request; a transient failure must not
// be charged to the model as a refusal.
func TestGenerateRetriesTheSameModelAfterAGatewayError(t *testing.T) {
	srv, seen := newServer(t, respondGatewayError, respondWithBase64)
	client := a6image.New("key", "gpt-image-2.5", "nano-banana-pro", srv.URL+"/v1", t.TempDir())

	path, err := client.Generate(context.Background(), "красный кот")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(*seen) != 2 {
		t.Fatalf("made %d requests, want the failed one retried", len(*seen))
	}
	if (*seen)[1].model != "gpt-image-2.5" {
		t.Errorf("retried with %q, want the primary model — a 502 is not a refusal", (*seen)[1].model)
	}
	if got := readResult(t, path); string(got) != string(pngBytes) {
		t.Errorf("saved %q, want the image from the retry", got)
	}
}
