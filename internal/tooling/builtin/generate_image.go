package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

type GenerateImage struct {
	generator output.ImageGenerator
}

func NewGenerateImage(generator output.ImageGenerator) *GenerateImage {
	return &GenerateImage{generator: generator}
}

func (g *GenerateImage) Name() string { return "generate_image" }

func (g *GenerateImage) Description() string {
	return "Draw an image, or rework the photo the user attached. Use it whenever the user " +
		"asks to draw, generate, edit, retouch or add something to a picture. " +
		"When a photo is attached (or one was sent earlier in this chat) the tool edits it by " +
		"default — pass edit_current=false only for an unrelated picture drawn from scratch. " +
		"Describe the wanted result in the prompt, in the user's own words plus any detail " +
		"needed to make it unambiguous. The image is sent to the user automatically: do not " +
		"paste the file path, just say what you did."
}

func (g *GenerateImage) Category() string { return "media" }

func (g *GenerateImage) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt": {
				"type": "string",
				"description": "What the resulting image must look like. For an edit, describe the change: \"add a pirate hat on the man\". Keep the user's language."
			},
			"edit_current": {
				"type": "boolean",
				"description": "Omit to edit the attached/last photo when there is one. Set false to draw a brand-new image and ignore any attached photo."
			}
		},
		"required": ["prompt"]
	}`)
}

type generateImageParams struct {
	Prompt      string `json:"prompt"`
	EditCurrent *bool  `json:"edit_current"`
}

func (g *GenerateImage) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p generateImageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}

	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is empty: describe what to draw")
	}

	current := imgctx.Current(ctx)
	// Omitted flag means "edit if there is something to edit" — with a photo in
	// hand, a drawing request is almost always about that photo.
	edit := current != ""
	if p.EditCurrent != nil {
		edit = *p.EditCurrent
	}

	var (
		path string
		err  error
	)
	switch {
	case edit && current == "":
		return nil, fmt.Errorf("no image to edit: ask the user to attach a photo")
	case edit:
		path, err = g.generator.Edit(ctx, current, prompt)
	default:
		path, err = g.generator.Generate(ctx, prompt)
	}
	if err != nil {
		return nil, err
	}

	imgctx.SinkFrom(ctx).Add(path)

	return json.Marshal(map[string]string{
		"status": "sent",
		"file":   path,
		"note":   "The image has already been delivered to the user.",
	})
}
