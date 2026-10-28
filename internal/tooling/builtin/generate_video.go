package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/olegmatyakubov/go-assistant/internal/adapter/driven/a6video"
	"github.com/olegmatyakubov/go-assistant/internal/port/output"
	"github.com/olegmatyakubov/go-assistant/internal/tooling/imgctx"
)

// A render usually takes 2-10 minutes, but suppliers sometimes sit at 95% for
// half an hour; the clip is already paid, so wait it out.
const videoRenderTimeout = 45 * time.Minute

// Each clip is paid per second, so cap how many can burn money at once.
const maxConcurrentVideos = 2

// gpt-image keeps faces recognisable up to a handful of references.
const maxComposeRefs = 6

var errNoVideoDelivery = errors.New("video generation works only inside a chat")

// GenerateVideo renders a clip from a prompt and the user's recent pictures.
//
// The video gateway honours a single opening frame only, so with several
// references the image model first blends them into that frame ("the cat from
// image 1 behind the wheel of the car from image 2"), and the video model
// animates it. The blended frame is shown to the user right away.
type GenerateVideo struct {
	generator      output.VideoGenerator
	composer       output.ImageComposer
	pricePerSecond float64
	slots          chan struct{}
}

// NewGenerateVideo builds the tool. composer may be nil: then only the latest
// reference is used. pricePerSecond (USD) is only reported; 0 hides it.
func NewGenerateVideo(generator output.VideoGenerator, composer output.ImageComposer, pricePerSecond float64) *GenerateVideo {
	return &GenerateVideo{
		generator:      generator,
		composer:       composer,
		pricePerSecond: pricePerSecond,
		slots:          make(chan struct{}, maxConcurrentVideos),
	}
}

func (g *GenerateVideo) Name() string { return "generate_video" }

func (g *GenerateVideo) Description() string {
	return "Generate a short video clip (4-15 s, 720p, with sound). Use it whenever the user asks " +
		"for a video, clip, animation, or to animate/bring to life pictures. " +
		"The user's recent photos and still frames of their GIFs/round videos are used automatically " +
		"as [Image 1], [Image 2], ... in the order listed in the request; with several of them an " +
		"opening frame combining them is drawn first. In the prompt, say what each reference " +
		"contributes (\"the man from [Image 1] in a clown costume rides the horse from [Image 2]\") " +
		"and then the action, camera and style. Write it in English. " +
		"Movement of a GIF/video cannot be copied — describe the motion in words instead. " +
		"The render takes a few minutes and the clip is sent to the user automatically — call the " +
		"tool once, then tell the user it is rendering. Do not call it again for the same request."
}

func (g *GenerateVideo) Category() string { return "media" }

func (g *GenerateVideo) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt": {
				"type": "string",
				"description": "English scene description. Name references by position: [Image 1], [Image 2]."
			},
			"seconds": {
				"type": "integer",
				"description": "Clip length, 4-15. Default 5 — longer costs proportionally more, use it only when asked."
			},
			"ratio": {
				"type": "string",
				"enum": ["16:9", "9:16", "1:1"],
				"description": "Aspect ratio. Omit for 16:9."
			},
			"use_references": {
				"type": "boolean",
				"description": "Omit to use the recent pictures. Set false for a clip unrelated to them."
			},
			"audio": {
				"type": "boolean",
				"description": "Generate sound. Default true."
			}
		},
		"required": ["prompt"]
	}`)
}

type generateVideoParams struct {
	Prompt        string `json:"prompt"`
	Seconds       int    `json:"seconds"`
	Ratio         string `json:"ratio"`
	UseReferences *bool  `json:"use_references"`
	Audio         *bool  `json:"audio"`
}

func (g *GenerateVideo) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p generateVideoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is empty: describe the clip")
	}

	deliver := imgctx.VideoDeliveryFrom(ctx)
	if deliver == nil {
		return nil, errNoVideoDelivery
	}

	var images []string
	markUsed := func() {}
	if p.UseReferences == nil || *p.UseReferences {
		refs := imgctx.RefsFrom(ctx)
		images = refs.Images
		// Nothing sent recently, but a picture is in play (e.g. one just drawn):
		// "now animate it" should animate that picture.
		if len(images) == 0 && imgctx.Current(ctx) != "" {
			images = []string{imgctx.Current(ctx)}
		}
		markUsed = refs.MarkUsed
	}

	// The prompt names a picture nobody sent (e.g. the set was lost): a clip
	// drawn from text alone is money spent on the wrong thing.
	if len(images) == 0 && strings.Contains(prompt, "[Image") {
		return nil, fmt.Errorf("no pictures available: the reference set is empty — ask the user to send the photo or video again, do not render without it")
	}

	select {
	case g.slots <- struct{}{}:
	default:
		return nil, fmt.Errorf("already rendering %d videos: wait for them to finish", maxConcurrentVideos)
	}
	release := func() { <-g.slots }

	start, composed, err := g.startFrame(ctx, images, prompt, p.Ratio)
	if err != nil {
		release()
		return nil, err
	}
	if composed {
		imgctx.SinkFrom(ctx).Add(start)
	}
	markUsed()

	req := output.VideoRequest{
		Prompt:     prompt,
		StartImage: start,
		Seconds:    a6video.ClampSeconds(p.Seconds),
		Ratio:      p.Ratio,
		Audio:      p.Audio == nil || *p.Audio,
	}

	// The chat request ends long before the render does, so the job gets its own context.
	go func() {
		defer release()
		jobCtx, cancel := context.WithTimeout(context.Background(), videoRenderTimeout)
		defer cancel()
		path, err := g.generator.GenerateVideo(jobCtx, req)
		if err != nil {
			slog.Error("video generation failed", "error", err)
		}
		deliver(path, err)
	}()

	result := map[string]any{
		"status":     "rendering",
		"seconds":    req.Seconds,
		"references": len(images),
		"eta":        "2-10 minutes",
		"note":       "The clip will be sent to the user automatically when ready. Do not call the tool again for this request.",
	}
	if composed {
		result["opening_frame"] = "drawn from the references and already shown to the user"
	}
	if g.pricePerSecond > 0 {
		result["estimated_cost_usd"] = math.Round(g.pricePerSecond*float64(req.Seconds)*100) / 100
	}
	return json.Marshal(result)
}

// startFrame picks the picture the clip opens on: none, the only reference,
// or a blend of several drawn by the image model.
func (g *GenerateVideo) startFrame(ctx context.Context, images []string, prompt, ratio string) (path string, composed bool, err error) {
	switch {
	case len(images) == 0:
		return "", false, nil
	case len(images) == 1 || g.composer == nil:
		return images[len(images)-1], false, nil
	}

	images = tail(images, maxComposeRefs)
	labels := make([]string, len(images))
	for i := range images {
		labels[i] = fmt.Sprintf("[Image %d]", i+1)
	}
	framePrompt := "Draw the opening frame of a video. The input images are " + strings.Join(labels, ", ") +
		", in this order. Scene: " + prompt + " Keep the people, faces and objects from the input images " +
		"recognisable, combine them in one coherent shot, no collage, no text."

	path, err = g.composer.Compose(ctx, images, framePrompt, frameSize(ratio))
	if err != nil {
		return "", false, fmt.Errorf("could not combine the references into one frame: %w", err)
	}
	return path, true, nil
}

func frameSize(ratio string) string {
	switch ratio {
	case "9:16":
		return "1024x1536"
	case "1:1":
		return "1024x1024"
	}
	return "1536x1024"
}

func tail(items []string, n int) []string {
	if len(items) > n {
		return items[len(items)-n:]
	}
	return items
}
