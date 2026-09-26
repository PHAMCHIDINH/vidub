package media

import (
	"context"
	"fmt"
	"math"
	"os"
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

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ffmpeg", append([]string{"-hide_banner", "-loglevel", "error", "-y"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

// makeVideo writes a test video of the given length, with or without audio.
func makeVideo(t *testing.T, path string, seconds string, withAudio bool) {
	args := []string{"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=" + seconds}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=220:duration="+seconds)
	}
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-shortest", path)
	requireEncoder(t, "libx264")
	ffmpeg(t, args...)
}

func requireEncoder(t *testing.T, name string) {
	t.Helper()
	out, _ := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	if !strings.Contains(string(out), " "+name+" ") {
		t.Skipf("ffmpeg has no %s", name)
	}
}

func makeTone(t *testing.T, path, seconds string) {
	ffmpeg(t, "-f", "lavfi", "-i", "sine=frequency=880:duration="+seconds, path)
}

func streams(t *testing.T, path string) string {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name",
		"-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

func TestMixFillsVideoLengthWhenSpeechIsShorter(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "in.mp4")
	speech := filepath.Join(dir, "speech.mp3")
	makeVideo(t, video, "6", true)
	makeTone(t, speech, "2") // much shorter than the video

	m := NewDubMixer()
	for _, tc := range []struct {
		name string
		mix  func(ctx context.Context, v, a, bgm, subs, out string) error
	}{
		{"voiceover", m.MixVoiceoverWithSubs},
		{"replacement", m.MixReplacementWithSubs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, tc.name+".mp4")
			if err := tc.mix(context.Background(), video, speech, "", "", out); err != nil {
				t.Fatal(err)
			}
			// Before the fix, amix duration=shortest cut the output to 2s.
			if err := m.VerifyOutput(out, 6); err != nil {
				t.Error(err)
			}
			if s := streams(t, out); !strings.Contains(s, "h264,video") || !strings.Contains(s, "aac,audio") {
				t.Errorf("streams = %q, want copied h264 video and aac audio", s)
			}
		})
	}
}

func TestReplacementWorksOnVideoWithoutAudio(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "silent.mp4")
	speech := filepath.Join(dir, "speech.mp3")
	makeVideo(t, video, "4", false)
	makeTone(t, speech, "2")

	m := NewDubMixer()
	out := filepath.Join(dir, "out.mp4")
	if err := m.MixReplacementWithSubs(context.Background(), video, speech, "", "", out); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyOutput(out, 4); err != nil {
		t.Error(err)
	}
}

func TestSubtitlesBurnInFromPathWithSpacesAndUnicode(t *testing.T) {
	requireFFmpeg(t)
	dir := filepath.Join(t.TempDir(), "đúp bing") // like this project's own path
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(dir, "in.mp4")
	speech := filepath.Join(dir, "speech.mp3")
	subs := filepath.Join(dir, "subtitle.srt")
	makeVideo(t, video, "3", true)
	makeTone(t, speech, "2")
	if err := GenerateSRT([]SubtitleSegment{{Text: "Xin chào", Start: 0.5, Duration: 1.5}}, subs); err != nil {
		t.Fatal(err)
	}

	m := NewDubMixer()
	out := filepath.Join(dir, "out.mp4")
	if err := m.MixVoiceoverWithSubs(context.Background(), video, speech, "", subs, out); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyOutput(out, 3); err != nil {
		t.Error(err)
	}
}

func TestVoiceKeepsItsVolume(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	video := filepath.Join(dir, "silent.mp4")
	speech := filepath.Join(dir, "speech.wav")
	makeVideo(t, video, "3", false)
	makeTone(t, speech, "3")

	m := NewDubMixer()
	out := filepath.Join(dir, "out.mp4")
	if err := m.MixReplacementWithSubs(context.Background(), video, speech, "", "", out); err != nil {
		t.Fatal(err)
	}
	in, outDB := meanVolume(t, speech), meanVolume(t, out)
	// amix's default normalization halved the voice: -6 dB.
	if math.Abs(in-outDB) > 1.5 {
		t.Errorf("mean volume %.1f dB in, %.1f dB out; the voice should keep its level", in, outDB)
	}
}

func meanVolume(t *testing.T, path string) float64 {
	out, _ := exec.Command("ffmpeg", "-hide_banner", "-i", path, "-af", "volumedetect", "-vn", "-f", "null", "-").CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "mean_volume:"); i >= 0 {
			var v float64
			if _, err := fmt.Sscan(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line[i+len("mean_volume:"):]), "dB")), &v); err == nil {
				return v
			}
		}
	}
	t.Fatalf("no mean_volume for %s:\n%s", path, out)
	return 0
}
