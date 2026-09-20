package output

import "context"

// ImageGenerator draws images and returns the path of the file it wrote.
type ImageGenerator interface {
	// Generate draws a new image from a text prompt.
	Generate(ctx context.Context, prompt string) (string, error)
	// Edit reworks an existing image according to the prompt.
	Edit(ctx context.Context, imagePath, prompt string) (string, error)
}
