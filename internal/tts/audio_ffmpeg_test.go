package tts

import (
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run real FFmpeg and are skipped where it is not installed.

func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

func makeTone(t *testing.T, path, seconds string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration="+seconds, path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}

func TestAlignAudioToWindow(t *testing.T) {
	requireFFmpeg(t)
	out, _ := exec.Command("ffmpeg", "-hide_banner", "-filters").Output()
	hasRubberband := strings.Contains(string(out), " rubberband ")

	for _, tc := range []struct {
		name, seconds string
		target        float64
		tier          string
		rubberband    bool
	}{
		{"fits already", "4", 5, "", false},
		{"tier 1 atempo", "6", 5, "aligned_t1", false},        // ratio 1.2
		{"tier 2 rubberband", "7", 5, "aligned_t2", true},     // ratio 1.4: 1.25 x 1.12
		{"fallback trim", "10", 5, "aligned_fallback", false}, // ratio 2.0
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rubberband && !hasRubberband {
				t.Skip("ffmpeg has no rubberband filter")
			}
			dir := t.TempDir()
			in := filepath.Join(dir, "narrative.mp3")
			makeTone(t, in, tc.seconds)

			got, err := AlignAudioToWindow(in, tc.target, dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.tier == "" {
				if got != in {
					t.Errorf("audio that fits was rewritten to %s", got)
				}
				return
			}
			if want := "narrative." + tc.tier + ".mp3"; filepath.Base(got) != want {
				t.Errorf("output %s, want %s (named after the input)", filepath.Base(got), want)
			}
			dur, err := GetAudioDuration(got)
			if err != nil {
				t.Fatal(err)
			}
			// The old rubberband call used tempo 2.0 here and produced ~2.8s.
			if math.Abs(dur-tc.target) > 0.25 {
				t.Errorf("aligned duration %.2fs, want about %.1fs", dur, tc.target)
			}
		})
	}
}

func TestAlignKeepsSeparateOutputsPerInput(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	narrative := filepath.Join(dir, "narrative.mp3")
	dub := filepath.Join(dir, "dub.mp3")
	makeTone(t, narrative, "6")
	makeTone(t, dub, "5.5")

	a, err := AlignAudioToWindow(narrative, 5, dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := AlignAudioToWindow(dub, 5, dir)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("both inputs aligned into %s; the second overwrote the first", a)
	}
}
