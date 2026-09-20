package builtin

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInspectSignatureRequiresPath(t *testing.T) {
	tool := NewInspectSignature()
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatalf("want error without path")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"/nonexistent/x.sig"}`)); err == nil {
		t.Fatalf("want error for missing file")
	}
}

func TestInspectSignatureSchemaAndName(t *testing.T) {
	tool := NewInspectSignature()
	if tool.Name() != "inspect_signature" {
		t.Fatalf("name: %s", tool.Name())
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
}
