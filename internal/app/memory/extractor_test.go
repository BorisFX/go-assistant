package memory

import "testing"

func TestParseFacts(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"none", "NONE", 0},
		{"none lowercase", "none", 0},
		{"empty", "", 0},
		{"bullets", "- user likes Go\n- user lives in Phnom Penh", 2},
		{"mixed prefix", "FACTS:\n* fact a\n- fact b\n  \n- fact c", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseFacts(c.in)
			if len(got) != c.want {
				t.Fatalf("parseFacts(%q) = %d facts, want %d (%v)", c.in, len(got), c.want, got)
			}
		})
	}
}
