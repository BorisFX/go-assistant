package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

type GenerateImage struct {
	generator output.ImageGenerator
	composer  output.ImageComposer
}

func NewGenerateImage(generator output.ImageGenerator) *GenerateImage {
	return &GenerateImage{generator: generator}
}

// WithComposer lets one request combine several of the user's photos;
// without it only the last photo can be edited.
func (g *GenerateImage) WithComposer(c output.ImageComposer) *GenerateImage {
	g.composer = c
	return g
}

func (g *GenerateImage) Name() string { return "generate_image" }

func (g *GenerateImage) Description() string {
	return "Draw an image, or rework the photo the user attached. Use it whenever the user " +
		"asks to draw, generate, edit, retouch or add something to a picture. " +
		"When a photo is attached (or one was sent earlier in this chat) the tool edits it by " +
		"default — pass edit_current=false only for an unrelated picture drawn from scratch. " +
		"Describe the wanted result in the prompt, in the user's own words plus any detail " +
		"needed to make it unambiguous. When the user sent several photos and wants them " +
		"combined (\"put the people from the first photo next to the girl from the second\"), " +
		"list them in images by their [Image N] numbers from the request. The image is sent to the user automatically: do not " +
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
			"images": {
				"type": "array",
				"items": {"type": "integer"},
				"description": "Which of the user's photos to use, by their [Image N] numbers, e.g. [1, 2]. Omit to use all photos the user just sent, or the last picture for a follow-up edit."
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
	Images      []int  `json:"images"`
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

	picked, err := pickImages(ctx, p.Images, current, edit)
	if err != nil {
		return nil, err
	}
	if len(picked) == 1 {
		current, edit = picked[0], true
	}

	var path string
	switch {
	case len(picked) > 1 && g.composer != nil:
		path, err = g.composer.Compose(ctx, picked, composePrompt(len(picked), prompt), "")
		if err == nil {
			imgctx.RefsFrom(ctx).MarkUsed()
		}
	case len(picked) > 1:
		// No composer: the newest photo is the best single guess.
		path, err = g.generator.Edit(ctx, picked[len(picked)-1], prompt)
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

// pickImages resolves which user photos feed the request. Explicit numbers win;
// otherwise a fresh batch of several photos is used whole, because editing only
// the newest one silently drops the people from the others.
func pickImages(ctx context.Context, numbers []int, current string, edit bool) ([]string, error) {
	refs := imgctx.RefsFrom(ctx).Images
	if len(numbers) > 0 {
		picked := make([]string, 0, len(numbers))
		for _, n := range numbers {
			if n < 1 || n > len(refs) {
				return nil, fmt.Errorf("no [Image %d]: the user has %d photos available", n, len(refs))
			}
			picked = append(picked, refs[n-1])
		}
		return tail(picked, maxComposeRefs), nil
	}
	if edit && len(refs) > 1 && slices.Contains(refs, current) {
		return tail(refs, maxComposeRefs), nil
	}
	return nil, nil
}

func composePrompt(n int, prompt string) string {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = fmt.Sprintf("[Image %d]", i+1)
	}
	return "The input images are " + strings.Join(labels, ", ") + ", in this order. " + prompt +
		" Keep the people, faces and objects from the input images recognisable; do not invent new people."
}
