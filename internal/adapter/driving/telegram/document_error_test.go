package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A dropped document must be reported as unread, not as a stack-trace-ish
// "Error: context deadline exceeded" the sender cannot act on.
func TestDocumentErrorTextOnTimeout(t *testing.T) {
	text := documentErrorText(context.DeadlineExceeded)
	if !strings.Contains(text, "непрочитанным") {
		t.Errorf("timeout must say the document was not read, got %q", text)
	}
	if strings.Contains(text, "context deadline") {
		t.Errorf("raw Go error leaked to the user: %q", text)
	}
}

func TestDocumentErrorTextKeepsOtherErrors(t *testing.T) {
	text := documentErrorText(errors.New("llm chat: 502"))
	if !strings.Contains(text, "502") {
		t.Errorf("real errors must stay visible, got %q", text)
	}
}
