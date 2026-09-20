package builtin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/olegmatyakubov/go-assistant/internal/tooling/builtin"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

type fakeImageGen struct {
	generatedPrompt string
	editedPath      string
	editedPrompt    string
	err             error
}

func (f *fakeImageGen) Generate(ctx context.Context, prompt string) (string, error) {
	f.generatedPrompt = prompt
	if f.err != nil {
		return "", f.err
	}
	return "/files/generated.png", nil
}

func (f *fakeImageGen) Edit(ctx context.Context, imagePath, prompt string) (string, error) {
	f.editedPath, f.editedPrompt = imagePath, prompt
	if f.err != nil {
		return "", f.err
	}
	return "/files/edited.png", nil
}

func TestGenerateImageDrawsFromPrompt(t *testing.T) {
	gen := &fakeImageGen{}
	tool := builtin.NewGenerateImage(gen)
	sink := &imgctx.Sink{}
	ctx := imgctx.WithSink(context.Background(), sink)

	out, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"красный кот на ноутбуке"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gen.generatedPrompt != "красный кот на ноутбуке" {
		t.Errorf("prompt = %q, want the user's prompt", gen.generatedPrompt)
	}
	if paths := sink.Paths(); len(paths) != 1 || paths[0] != "/files/generated.png" {
		t.Errorf("sink = %v, want the generated image queued for sending", paths)
	}
	if !strings.Contains(string(out), "generated.png") {
		t.Errorf("result = %s, want it to name the produced file", out)
	}
}

func TestGenerateImageEditsTheAttachedPhoto(t *testing.T) {
	gen := &fakeImageGen{}
	tool := builtin.NewGenerateImage(gen)
	sink := &imgctx.Sink{}
	ctx := imgctx.WithSink(imgctx.WithCurrent(context.Background(), "/files/photo.jpg"), sink)

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"добавь пиратскую шляпу","edit_current":true}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gen.editedPath != "/files/photo.jpg" {
		t.Errorf("edited %q, want the attached photo", gen.editedPath)
	}
	if gen.editedPrompt != "добавь пиратскую шляпу" {
		t.Errorf("edit prompt = %q, want the user's instruction", gen.editedPrompt)
	}
	if paths := sink.Paths(); len(paths) != 1 || paths[0] != "/files/edited.png" {
		t.Errorf("sink = %v, want the edited image queued for sending", paths)
	}
}

func TestGenerateImageRefusesToEditWithoutAnImage(t *testing.T) {
	gen := &fakeImageGen{}
	tool := builtin.NewGenerateImage(gen)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"добавь шляпу","edit_current":true}`))
	if err == nil {
		t.Fatal("Execute succeeded without an image, want an error the model can relay")
	}
	if !strings.Contains(err.Error(), "no image") {
		t.Errorf("error = %v, want it to say there is no image to edit", err)
	}
	if gen.editedPath != "" {
		t.Error("called Edit despite having no image")
	}
}

func TestGenerateImageRequiresAPrompt(t *testing.T) {
	tool := builtin.NewGenerateImage(&fakeImageGen{})

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"  "}`)); err == nil {
		t.Fatal("Execute succeeded on a blank prompt, want an error")
	}
}

// An edit request that arrives with a photo but without the flag is still an edit:
// the model forgets the flag, and silently drawing something unrelated is worse.
func TestGenerateImageEditsWhenAPhotoIsAttachedEvenWithoutTheFlag(t *testing.T) {
	gen := &fakeImageGen{}
	tool := builtin.NewGenerateImage(gen)
	ctx := imgctx.WithCurrent(context.Background(), "/files/photo.jpg")

	if _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"добавь шляпу"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if gen.editedPath != "/files/photo.jpg" {
		t.Errorf("edited %q, want the attached photo", gen.editedPath)
	}
	if gen.generatedPrompt != "" {
		t.Error("drew a new image instead of editing the attached photo")
	}
}
