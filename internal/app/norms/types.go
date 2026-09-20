// Package norms is the normative corpus: full texts of laws, government
// decrees and building codes (СП/ГОСТ) split into legal units and indexed for
// exact lookup by reference and for semantic search. A legal conclusion may
// cite a norm only when its text came from here, never from model memory.
package norms

import (
	"context"
	"time"
)

// Document is one normative act in the corpus. Code is how the text is cited
// ("218-ФЗ", "ГрК РФ", "СП 4.13130.2013", "ПП 87"); Edition pins the wording
// the chunks were taken from, because codes are amended and a citation without
// an edition cannot be verified.
type Document struct {
	Code       string
	Title      string
	Edition    string
	Source     string
	FileHash   string
	IngestedAt time.Time
}

// Chunk is one legal unit (or a piece of a long one) of a document. Ref is
// the citable unit id: "ст. 26 ч. 1 п. 7", "п. 4.3", "Приложение А". Ord keeps
// document order so pieces of a split unit are read in sequence.
type Chunk struct {
	ID        int64
	DocCode   string
	Ref       string
	Ord       int
	Text      string
	Embedding []float32
}

// Key identifies a chunk without a database id, for de-duplication of hits.
func (c Chunk) Key() string { return c.DocCode + "|" + c.Ref + "|" + itoa(c.Ord) }

// Repository is the storage the corpus needs. Declared here, implemented by
// the postgres adapter, so the package tests with an in-memory fake.
type Repository interface {
	UpsertDocument(ctx context.Context, d Document) error
	// ReplaceChunks drops the document's previous chunks and stores the new
	// set. RefNorm (normalized Ref) is derived by the implementation through
	// NormalizeRef so lookups never depend on how a ref was spelled.
	ReplaceChunks(ctx context.Context, docCode string, chunks []Chunk) error
	// FindByRef returns chunks whose normalized ref starts with refPrefix
	// (already normalized), in document order, at most limit.
	FindByRef(ctx context.Context, docCode, refPrefix string, limit int) ([]Chunk, error)
	SearchSimilar(ctx context.Context, embedding []float32, limit int) ([]Chunk, error)
	ListDocuments(ctx context.Context) ([]Document, error)
	// DocumentHash returns the stored file hash, or "" when unknown.
	DocumentHash(ctx context.Context, code string) (string, error)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
