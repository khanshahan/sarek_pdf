package layout

import (
	"reflect"
	"testing"
)

func TestCollapseDoubleRules(t *testing.T) {
	cases := map[string][2][]float64{
		"double frame":       {{71, 76, 519, 524}, {71, 524}},
		"double inner":       {{0, 100, 103, 200}, {0, 100, 200}},
		"normal grid":        {{0, 50, 100, 150}, {0, 50, 100, 150}},
		"double bottom edge": {{0, 20, 40, 42}, {0, 20, 42}},
	}
	for name, c := range cases {
		if got := collapseDoubleRules(c[0]); !reflect.DeepEqual(got, c[1]) {
			t.Errorf("%s: %v → %v, want %v", name, c[0], got, c[1])
		}
	}
}
