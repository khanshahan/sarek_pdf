package correct

import (
	"context"
	"testing"
)

func TestVerifyAcceptsLocalRepairs(t *testing.T) {
	cases := [][2]string{
		{"The exam ple shows the resu lt", "The example shows the result"},                 // merges
		{"theman went home", "the man went home"},                                          // split
		{"modem comrnunication is fast", "modem communication is fast"},                    // rn -> m
		{"Revenue grew by 12.5 percent in 2024.", "Revenue grew by 12.5 percent in 2024."}, // unchanged
		{"the vvorld is Iarge", "the world is large"},                                      // vv/w and I/l confusions
		{"cafe resume", "café résumé"},                                                     // lost diacritics
	}
	for _, c := range cases {
		if _, ok := Verify(c[0], c[1]); !ok {
			t.Errorf("rejected valid repair %q -> %q", c[0], c[1])
		}
	}
}

func TestVerifyRejectsHallucinations(t *testing.T) {
	cases := [][2]string{
		{"The payment is due on Monday", "The payment is not due on Monday"},      // inserted word
		{"The payment is due on Monday", "The payment is due Monday"},             // deleted word
		{"Total: 1,234 EUR", "Total: 1,243 EUR"},                                  // changed digits
		{"Total: 1,234 EUR", "Total: 1,234 USD"},                                  // changed unit
		{"The cat sat on the mat", "The dog sat on the mat"},                      // substituted word
		{"we accept the terms", "we reject the terms"},                            // meaning flip
		{"Section A applies", "Section A applies to all customers in the region"}, // completion
		{"alpha beta gamma", "gamma beta alpha"},                                  // reorder
		{"short text here", ""},                                                   // emptied
		{"teh quick brown fox", "the quick brown fox"},                            // spelling fix (not an extraction error)
		{"w\uFFFDrd count", "word count"},                                         // guessing an unmapped glyph
	}
	for _, c := range cases {
		if _, ok := Verify(c[0], c[1]); ok {
			t.Errorf("accepted hallucination %q -> %q", c[0], c[1])
		}
	}
}

func TestVerifyAlignment(t *testing.T) {
	align, ok := Verify("a bc de f", "a bcde f")
	if !ok {
		t.Fatal("rejected")
	}
	want := []int{0, 1, 3}
	for i := range want {
		if align[i] != want[i] {
			t.Fatalf("align = %v, want %v", align, want)
		}
	}
}

func TestRules(t *testing.T) {
	got := Rules{}.Apply("ﬁnancial  e­xample​ text ⽤")
	if got != "financial example text 用" {
		t.Errorf("Rules.Apply = %q", got)
	}
}

func TestChainDiscardsUnverified(t *testing.T) {
	bad := Func(func(_ context.Context, in []string) ([]string, error) {
		out := make([]string, len(in))
		for i := range in {
			out[i] = in[i] + " and more words"
		}
		return out, nil
	})
	out, err := Chain(bad).Correct(context.Background(), []string{"keep this text"})
	if err != nil || out[0] != "keep this text" {
		t.Errorf("Chain = %q, %v", out, err)
	}
}
