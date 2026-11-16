package correct

import (
	"context"
	"strings"

	"github.com/khanshahan/sarek_pdf/internal/textnorm"
)

// Rules is a deterministic, dependency-free corrector. It only performs
// lossless normalisations that cannot change meaning:
//
//   - typographic ligatures are expanded (ﬁ → fi, ﬂ → fl, …)
//   - zero-width characters and soft hyphens are removed
//   - runs of spaces are collapsed
//   - CJK radicals / compatibility ideographs become unified ideographs
type Rules struct{}

var ruleReplacer = strings.NewReplacer(
	"ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi", "ﬄ", "ffl",
	"ﬅ", "st", "ﬆ", "st",
	"\u200B", "", "\u200C", "", "\u200D", "", "\uFEFF", "", "\u00AD", "",
	"\u00A0", " ",
)

// Apply normalises one string.
func (Rules) Apply(s string) string {
	s = textnorm.String(ruleReplacer.Replace(s))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// Correct implements Corrector.
func (r Rules) Correct(_ context.Context, in []string) ([]string, error) {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = r.Apply(s)
	}
	return out, nil
}
