package media

import (
	"strings"
	"testing"
)

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestBuildArgsCopiesVideoWithoutSubtitles(t *testing.T) {
	m := &DubMixer{VideoEncoder: "libx264"}
	args := m.buildArgs("in.mp4", "tts.mp3", "", "", "out.mp4", "replacement", false, false)

	if got := argAfter(args, "-c:v"); got != "copy" {
		t.Errorf("-c:v = %q, want copy when nothing is drawn on the video", got)
	}
}

func TestBuildArgsEncoderSettings(t *testing.T) {
	for _, tt := range []struct {
		encoder     string
		want, avoid []string
	}{
		{"libx264", []string{"-crf", "-preset"}, []string{"-b:v"}},
		{"libopenh264", []string{"-b:v"}, []string{"-crf", "-preset"}}, // it ignores crf/preset
	} {
		m := &DubMixer{VideoEncoder: tt.encoder}
		args := m.buildArgs("in.mp4", "tts.mp3", "", "subs.srt", "out.mp4", "voiceover", false, true)
		joined := " " + strings.Join(args, " ") + " "

		if got := argAfter(args, "-c:v"); got != tt.encoder {
			t.Errorf("%s: -c:v = %q", tt.encoder, got)
		}
		for _, f := range tt.want {
			if !strings.Contains(joined, " "+f+" ") {
				t.Errorf("%s: missing %s in %v", tt.encoder, f, args)
			}
		}
		for _, f := range tt.avoid {
			if strings.Contains(joined, " "+f+" ") {
				t.Errorf("%s: unexpected %s in %v", tt.encoder, f, args)
			}
		}
	}
}

func TestBuildArgsAudioGraph(t *testing.T) {
	m := &DubMixer{VideoEncoder: "libx264"}

	voiceover := argAfter(m.buildArgs("in.mp4", "tts.mp3", "", "", "out.mp4", "voiceover", false, false), "-filter_complex")
	for _, want := range []string{"[0:a]volume=0.15", "normalize=0", "apad"} {
		if !strings.Contains(voiceover, want) {
			t.Errorf("voiceover graph %q missing %q", voiceover, want)
		}
	}

	replacement := argAfter(m.buildArgs("in.mp4", "tts.mp3", "", "", "out.mp4", "replacement", false, false), "-filter_complex")
	if strings.Contains(replacement, "[0:a]") {
		t.Errorf("replacement graph %q reads the original audio; it must work on videos without audio", replacement)
	}
	if !strings.Contains(replacement, "apad") {
		t.Errorf("replacement graph %q must pad to the video length", replacement)
	}
}
