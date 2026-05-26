package output

import "context"

type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}
