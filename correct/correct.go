// Package correct defines the pluggable text-correction module and the
// strict guard that enforces the zero-hallucination rule
// (CONSTITUTION.md, Article 3).
//
// A Corrector proposes corrected text; the library only accepts a
// proposal if Verify proves it is a local repair of the original:
//
//   - every output word aligns to an input word, or to a merge of up to
//     three adjacent input words, or to part of a split input word;
//   - aligned words differ only by known extraction artefacts (case,
//     diacritics, hyphens, ligatures, rn↔m, cl↔d, vv↔w, I↔l);
//   - no word is inserted or deleted;
//   - the sequence of digits is unchanged;
//   - the total character edit budget is small.
//
// Anything else is rejected and the original text is kept.
package correct

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Corrector proposes corrections for a batch of paragraphs. It must
// return a slice of the same length; an element may be returned unchanged.
// Implementations must be safe for concurrent use.
type Corrector interface {
	Correct(ctx context.Context, paragraphs []string) ([]string, error)
}

// Func adapts a function to the Corrector interface.
type Func func(ctx context.Context, paragraphs []string) ([]string, error)

// Correct implements Corrector.
func (f Func) Correct(ctx context.Context, p []string) ([]string, error) { return f(ctx, p) }

// Chain applies correctors in order, verifying each step.
func Chain(cs ...Corrector) Corrector {
	return Func(func(ctx context.Context, in []string) ([]string, error) {
		cur := append([]string(nil), in...)
		for _, c := range cs {
			out, err := c.Correct(ctx, cur)
			if err != nil {
				return cur, err
			}
			for i := range cur {
				if i < len(out) {
					if _, ok := Verify(cur[i], out[i]); ok {
						cur[i] = out[i]
					}
				}
			}
		}
		return cur, nil
	})
}

// Limits for Verify.
const (
	maxMerge = 3
)

// Verify checks that corrected is an acceptable local repair of orig.
// It returns, for each word of corrected, the index of the original word
// it derives from.
func Verify(orig, corrected string) ([]int, bool) {
	ow := strings.Fields(orig)
	nw := strings.Fields(corrected)
	if len(ow) == 0 || len(nw) == 0 {
		return nil, len(ow) == len(nw)
	}
	if digits(orig) != digits(corrected) {
		return nil, false
	}
	// Global budget on non-whitespace characters.
	// Measured on skeletons so that permitted artefact fixes are free.
	so, sn := skeleton(squash(orig)), skeleton(squash(corrected))
	budget := max(2, utf8.RuneCountInString(so)/20)
	if abs(utf8.RuneCountInString(so)-utf8.RuneCountInString(sn)) > budget {
		return nil, false
	}
	if levenshtein(so, sn, budget+1) > budget {
		return nil, false
	}
	// Word alignment DP: reach[i][j] = previous state, -1 if unreachable.
	n, m := len(ow), len(nw)
	if n*m > 4_000_000 {
		return nil, false
	}
	type step struct{ pi, pj int }
	prev := make([][]step, n+1)
	for i := range prev {
		prev[i] = make([]step, m+1)
		for j := range prev[i] {
			prev[i][j] = step{-1, -1}
		}
	}
	prev[0][0] = step{0, 0}
	for i := 0; i <= n; i++ {
		for j := 0; j <= m; j++ {
			if prev[i][j].pi < 0 || (i == n && j == m) {
				continue
			}
			// 1:1 (same or similar)
			if i < n && j < m && similar(ow[i], nw[j]) && prev[i+1][j+1].pi < 0 {
				prev[i+1][j+1] = step{i, j}
			}
			// k:1 merge ("exam ple" -> "example")
			for k := 2; k <= maxMerge && i+k <= n; k++ {
				if j < m && similar(strings.Join(ow[i:i+k], ""), nw[j]) && prev[i+k][j+1].pi < 0 {
					prev[i+k][j+1] = step{i, j}
				}
			}
			// 1:k split ("theman" -> "the man")
			for k := 2; k <= maxMerge && j+k <= m; k++ {
				if i < n && similar(ow[i], strings.Join(nw[j:j+k], "")) && prev[i+1][j+k].pi < 0 {
					prev[i+1][j+k] = step{i, j}
				}
			}
		}
	}
	if prev[n][m].pi < 0 {
		return nil, false
	}
	align := make([]int, m)
	for i, j := n, m; i > 0 || j > 0; {
		p := prev[i][j]
		for k := p.pj; k < j; k++ {
			align[k] = p.pi
		}
		i, j = p.pi, p.pj
	}
	return align, true
}

// similar reports whether two words are equal up to differences that
// PDF text extraction is known to produce: letter case, diacritics,
// hyphens/soft hyphens, ligatures and glyph confusions (rn↔m, cl↔d,
// vv↔w, I↔l). Arbitrary spelling changes are rejected on purpose: they
// could alter meaning ("accept" → "except").
func similar(a, b string) bool {
	return a == b || skeleton(a) == skeleton(b)
}

var confusions = strings.NewReplacer("rn", "m", "cl", "d", "vv", "w")

func skeleton(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '-', '\u00AD', '\u2010', '\u2011', '\u200B':
			continue
		case 'I', '|':
			r = 'l'
		case '\uFB00':
			sb.WriteString("ff")
			continue
		case '\uFB01':
			sb.WriteString("fi")
			continue
		case '\uFB02':
			sb.WriteString("fl")
			continue
		case '\uFB03':
			sb.WriteString("ffi")
			continue
		case '\uFB04':
			sb.WriteString("ffl")
			continue
		}
		if b, ok := baseLetter[r]; ok {
			r = b
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return confusions.Replace(sb.String())
}

func stripPunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) })
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func squash(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// levenshtein computes the edit distance between a and b, stopping early
// once it is known to exceed limit (returns limit in that case).
func levenshtein(a, b string, limit int) int {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) >= limit {
		return limit
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	return min(prev[len(rb)], limit)
}
