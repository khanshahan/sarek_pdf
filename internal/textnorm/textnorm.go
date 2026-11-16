// Package textnorm holds deterministic, meaning-preserving text
// normalisations shared by extraction, search and export.
package textnorm

import "strings"

// tatweel (U+0640) is a purely typographic Arabic elongation mark.
const tatweel = 'ـ'

// Expand returns the normalised form of r: CJK radicals and compatibility
// ideographs become unified ideographs (U+2F64 ⽤ → 用), Arabic
// presentation forms become base letters (ﻣ → م), tatweel is dropped.
// It returns nil when r is unchanged.
func Expand(r rune) []rune {
	if r < 0x0640 || (r > 0x0640 && r < 0x2E80) {
		return nil
	}
	if r == tatweel {
		return []rune{}
	}
	if s, ok := compat[r]; ok {
		return []rune(s)
	}
	return nil
}

// String normalises every character of s.
func String(s string) string {
	if !needs(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if e := Expand(r); e != nil {
			sb.WriteString(string(e))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func needs(s string) bool {
	for _, r := range s {
		if Expand(r) != nil {
			return true
		}
	}
	return false
}

// IsCJK reports whether r is a CJK ideograph, kana or hangul syllable —
// scripts written without spaces between words.
func IsCJK(r rune) bool {
	return r >= 0x2E80 && r <= 0x9FFF || r >= 0xAC00 && r <= 0xD7AF ||
		r >= 0xF900 && r <= 0xFAFF || r >= 0xFF00 && r <= 0xFFEF || r >= 0x20000 && r <= 0x3FFFF
}
