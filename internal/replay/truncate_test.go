package replay

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateNeverSplitsARune walks every cut position across strings built
// from 2-, 3- and 4-byte runes.
//
// This is the failure the function exists to prevent: Postgres rejects an
// invalid UTF-8 byte sequence in a text column, so a preview cut mid-rune made
// the whole checkpoint insert fail — meaning any command that printed non-ASCII
// output could not be checkpointed at all.
func TestTruncateNeverSplitsARune(t *testing.T) {
	inputs := map[string]string{
		"2-byte runes": strings.Repeat("é", 400),
		"3-byte runes": strings.Repeat("日", 400),
		"4-byte runes": strings.Repeat("𝄞", 400),
		"mixed":        strings.Repeat("aé日𝄞", 100),
		"emoji":        strings.Repeat("🔐", 300),
	}

	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			for cut := 0; cut <= len(in); cut++ {
				out := truncate(in, cut)

				if !utf8.ValidString(out) {
					t.Fatalf("truncate(%s, %d) produced invalid UTF-8", name, cut)
				}
				if len(out) > cut {
					t.Fatalf("truncate(%s, %d) returned %d bytes, over the limit", name, cut, len(out))
				}
				if !strings.HasPrefix(in, out) {
					t.Fatalf("truncate(%s, %d) is not a prefix of the input", name, cut)
				}
			}
		})
	}
}

// TestTruncateKeepsAsciiExact confirms the common case is untouched: an ASCII
// preview is cut at exactly the limit, so the change to rune-awareness did not
// cost any characters.
func TestTruncateKeepsAsciiExact(t *testing.T) {
	in := strings.Repeat("x", 1000)

	if got := truncate(in, 500); len(got) != 500 {
		t.Errorf("ASCII truncate kept %d bytes, want exactly 500", len(got))
	}
	if got := truncate("short", 500); got != "short" {
		t.Errorf("truncate returned %q for a string under the limit", got)
	}
	if got := truncate("", 500); got != "" {
		t.Errorf("truncate of empty string returned %q", got)
	}
}

// TestTruncateDropsAtMostOneRune bounds the data loss: rounding down to a rune
// boundary must never discard more than the single partial character.
func TestTruncateDropsAtMostOneRune(t *testing.T) {
	in := strings.Repeat("𝄞", 200) // 4 bytes per rune

	for cut := 4; cut <= len(in); cut++ {
		out := truncate(in, cut)
		if lost := cut - len(out); lost > 3 {
			t.Fatalf("truncate(cut=%d) discarded %d bytes, more than one partial rune", cut, lost)
		}
	}
}
