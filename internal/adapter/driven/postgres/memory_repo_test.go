package postgres

import "testing"

func TestNullableVectorEmptyIsSQLNull(t *testing.T) {
	got := nullableVector(nil)
	if got.Valid {
		t.Fatalf("empty embedding must map to SQL NULL, got %+v", got)
	}
}

func TestNullableVectorFormatsPgvectorLiteral(t *testing.T) {
	got := nullableVector([]float32{0.5, -1.25})
	if !got.Valid {
		t.Fatal("expected valid NullString for non-empty vector")
	}
	want := "[0.500000,-1.250000]"
	if got.String != want {
		t.Errorf("got %q, want %q", got.String, want)
	}
}
