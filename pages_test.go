package sarekpdf

import (
	"reflect"
	"testing"
)

func TestParsePageRanges(t *testing.T) {
	ok := map[string][]int{
		"":        {1, 2, 3},
		"2":       {2},
		"1-2,3":   {1, 2, 3},
		"2-":      {2, 3},
		"-2":      {1, 2},
		"1-10":    {1, 2, 3}, // clamped
		" 3 , 1 ": {3, 1},
	}
	for spec, want := range ok {
		got, err := ParsePageRanges(spec, 3)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, %v; want %v", spec, got, err, want)
		}
	}
	for _, spec := range []string{"4", "0", "5-7", "3-1", "x", "1-a"} {
		if _, err := ParsePageRanges(spec, 3); err == nil {
			t.Errorf("%q: expected error", spec)
		}
	}
}
