package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/olegmatyakubov/go-assistant/internal/app/signature"
)

// InspectSignature parses a detached CMS/PKCS7 signature (.sig, DER) and reports
// the real signers (from leaf certificates) and the number of signatures. It lets
// the model verify "who signed" from facts instead of guessing by file name.
// The parsing itself lives in internal/app/signature so the review pipeline
// reads .sig files through the same code.
type InspectSignature struct{}

func NewInspectSignature() *InspectSignature { return &InspectSignature{} }

func (s *InspectSignature) Name() string { return "inspect_signature" }

func (s *InspectSignature) Description() string {
	return "Inspect a detached electronic signature file (.sig, CMS/PKCS7, DER) on the server: returns the real signers (names/orgs from leaf certificates) and how many signatures it holds. Use this instead of guessing signers from the file name. Russian GOST signatures are supported (certificate subjects are read; the crypto is not verified)."
}

func (s *InspectSignature) Category() string { return "documents" }

func (s *InspectSignature) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Local server path to a .sig file (detached CMS/PKCS7 signature, DER). Download or unzip it first if it is not already on disk."
			}
		},
		"required": ["path"]
	}`)
}

type inspectSigParams struct {
	Path string `json:"path"`
}

func (s *InspectSignature) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p inspectSigParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("parse params: %w", err)
	}
	if p.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	info, err := signature.Inspect(ctx, p.Path)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"path":            info.Path,
		"signature_count": info.SignatureCount,
		"signers":         info.Signers,
		"crypto_notice":   signature.CryptoNotice,
		"instruction":     "signature_count is the number of real signatures. 'signers' are the actual signing persons/organizations (leaf certificates), NOT certificate-authority chain certs. State signers only from this data; never infer them from the file name. If a document carries a second signature from a government body, that is the issuer's own signature and is normal.",
	}
	if info.Warning != "" {
		result["warning"] = info.Warning
	}
	return json.Marshal(result)
}
