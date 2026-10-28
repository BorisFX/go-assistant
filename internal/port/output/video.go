package output

import "context"

// VideoRequest describes one clip to generate.
type VideoRequest struct {
	Prompt string
	// StartImage is a local picture the clip opens on; empty means text-to-video.
	// The gateway takes exactly one image, so several references are blended
	// into this frame beforehand.
	StartImage string
	Seconds    int
	// Ratio is "16:9", "9:16", "1:1", ... or "" for the default.
	Ratio string
	Audio bool
}

// VideoGenerator renders a clip and returns the path of the mp4 it wrote.
type VideoGenerator interface {
	GenerateVideo(ctx context.Context, req VideoRequest) (string, error)
}
