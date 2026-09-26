package translator

import "sort"

// BudgetCharsPerSecond is how many characters of Vietnamese speech fit in
// one second of video: about 15 chars/s at normal Edge TTS speed, plus the
// 10% speed-up that still sounds natural. Measured on real captions:
// HoaiMy 16.0 and NamMinh 15.7 chars/s, so this errs slightly short. scripts/tts.py prints the measured
// rate after each dub ("Measured speech rate"), use it to tune this value.
const BudgetCharsPerSecond = 16.5

// minCharBudget keeps very short captions translatable.
const minCharBudget = 12

// SetCharBudgets sets MaxChars on every segment from the time it has before
// the next segment starts. The translator is asked to stay under it, because
// a translation longer than its caption can be spoken only by speeding up or
// cutting the dub.
func SetCharBudgets(segs []Segment) {
	order := make([]int, len(segs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return segs[order[a]].Start < segs[order[b]].Start })

	for k, i := range order {
		window := segs[i].Duration
		// Captions often overlap: speech must end when the next one starts.
		for _, j := range order[k+1:] {
			if segs[j].Start > segs[i].Start {
				window = min(window, segs[j].Start-segs[i].Start)
				break
			}
		}
		segs[i].MaxChars = max(minCharBudget, int(window*BudgetCharsPerSecond))
	}
}
