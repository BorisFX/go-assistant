package output

import "context"

// SpeechTranscriber turns a downloaded voice message into text.
type SpeechTranscriber interface {
	Transcribe(ctx context.Context, filePath string) (string, error)
}
