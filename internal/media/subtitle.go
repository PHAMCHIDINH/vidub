package media

import (
	"fmt"
	"os"
	"strings"
)

type SubtitleSegment struct {
	Text     string
	Start    float64
	Duration float64
}

func GenerateSRT(segments []SubtitleSegment, outputPath string) error {
	var b strings.Builder
	idx := 0

	for _, seg := range segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		if seg.Duration <= 0 {
			continue
		}
		idx++
		b.WriteString(fmt.Sprintf("%d\n", idx))
		b.WriteString(fmt.Sprintf("%s --> %s\n",
			formatSRTTime(seg.Start),
			formatSRTTime(seg.Start+seg.Duration),
		))
		b.WriteString(text + "\n\n")
	}

	if idx == 0 {
		return fmt.Errorf("no valid subtitle segments")
	}

	return os.WriteFile(outputPath, []byte(b.String()), 0644)
}

func formatSRTTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}
