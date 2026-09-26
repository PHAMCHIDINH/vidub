package translator

import "testing"

func TestSetCharBudgets(t *testing.T) {
	segs := []Segment{
		{Text: "b", Start: 2.0, Duration: 3.0}, // overlaps the next caption: window 1s
		{Text: "a", Start: 0.0, Duration: 1.0}, // out of order on purpose: window 1s
		{Text: "c", Start: 3.0, Duration: 2.0}, // last: its own duration
		{Text: "d", Start: 3.0, Duration: 0.1}, // same start as c
	}
	SetCharBudgets(segs)

	budget := func(seconds float64) int { return int(seconds * BudgetCharsPerSecond) }
	want := map[string]int{
		"a": budget(1.0),
		"b": budget(1.0),
		"c": budget(2.0),
		"d": minCharBudget,
	}
	for _, s := range segs {
		if s.MaxChars != want[s.Text] {
			t.Errorf("segment %q: MaxChars = %d, want %d", s.Text, s.MaxChars, want[s.Text])
		}
	}
}
