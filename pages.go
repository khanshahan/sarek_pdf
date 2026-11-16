package sarekpdf

import (
	"fmt"
	"strconv"
	"strings"
)

// ParsePageRanges parses "1-3,5,8-" into 1-based page numbers for a
// document with n pages. An empty spec selects all pages.
func ParsePageRanges(spec string, n int) ([]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi := part, part
		if i := strings.IndexByte(part, '-'); i >= 0 {
			lo, hi = strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:])
			if lo == "" {
				lo = "1"
			}
			if hi == "" {
				hi = strconv.Itoa(n)
			}
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || a < 1 || b > n || a > b {
			return nil, fmt.Errorf("sarekpdf: invalid page range %q for %d pages", part, n)
		}
		for p := a; p <= b; p++ {
			out = append(out, p)
		}
	}
	return out, nil
}
