package tts

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// EdgeTTSClient calls Microsoft Edge TTS via the edge-tts CLI to generate
// speech audio. The edge-tts Python package must be installed on the system.
//
// Implementation approach (Option A from spec):
// Exec the edge-tts CLI tool for simplicity and reliability. This matches the
// existing Python worker behavior exactly (python-worker/utils/audio.py:_tts_single).
type EdgeTTSClient struct {
	// DefaultVoice is the fallback voice used when Synthesize is called with an
	// empty voice string.
	DefaultVoice string

	// DefaultRate is the TTS speaking rate passed to edge-tts (e.g. "+0%", "+20%").
	DefaultRate string

	// DefaultPitch is the TTS pitch passed to edge-tts (e.g. "+0Hz", "-10%").
	DefaultPitch string
}

// NewEdgeTTSClient creates a new EdgeTTSClient with sensible defaults.
//   - defaultVoice: e.g. "vi-VN-HoaiMyNeural"
//   - defaultRate: e.g. "+0%" (no speed adjustment)
//   - defaultPitch: e.g. "+0Hz" (no pitch adjustment)
func NewEdgeTTSClient(defaultVoice, defaultRate, defaultPitch string) *EdgeTTSClient {
	if defaultVoice == "" {
		defaultVoice = "vi-VN-HoaiMyNeural"
	}
	if defaultRate == "" {
		defaultRate = "+0%"
	}
	if defaultPitch == "" {
		defaultPitch = "+0Hz"
	}
	return &EdgeTTSClient{
		DefaultVoice: defaultVoice,
		DefaultRate:  defaultRate,
		DefaultPitch: defaultPitch,
	}
}

// Synthesize calls Microsoft Edge TTS to generate speech for a single text
// segment. The output is written to outputPath as an MP3 file.
//
// Retry behavior: 3 attempts with exponential backoff (2s, 4s, 8s) per the
// spec (section 2.5). Each attempt re-runs the entire edge-tts command.
// Output file is verified to exist and be non-empty after each attempt.
//
// Parameters:
//   - ctx: context for cancellation and timeouts (should allow >= 30s per call)
//   - text: the text to synthesize (may include punctuation for natural pauses)
//   - voice: the Edge TTS voice name (e.g. "vi-VN-HoaiMyNeural"); empty => DefaultVoice
//   - rate: speaking rate (e.g. "+0%", "+20%"); empty => DefaultRate
//   - pitch: speaking pitch (e.g. "+0Hz"); empty => DefaultPitch
//   - outputPath: absolute path for the output MP3 file
func (c *EdgeTTSClient) Synthesize(ctx context.Context, text, voice, rate, pitch, outputPath string) error {
	if text == "" {
		return fmt.Errorf("empty text, nothing to synthesize")
	}

	if voice == "" {
		voice = c.DefaultVoice
	}
	if rate == "" {
		rate = c.DefaultRate
	}
	if pitch == "" {
		pitch = c.DefaultPitch
	}

	// Ensure output directory exists.
	dir := dirName(outputPath)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create output directory %s: %w", dir, err)
		}
	}

	backoffs := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoffs[attempt-1]):
			case <-ctx.Done():
				return fmt.Errorf("tts context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		err := c.synthesizeOnce(ctx, text, voice, rate, pitch, outputPath)
		if err == nil {
			// Verify output file was actually created and is non-empty.
			if info, statErr := os.Stat(outputPath); statErr == nil && info.Size() > 0 {
				return nil
			}
			err = fmt.Errorf("output file %s was not created or is empty", outputPath)
		}

		// Continue retrying unless last attempt.
		if attempt < 2 {
			continue
		}
	}

	return fmt.Errorf("edge-tts failed after 3 retries for text (len=%d, voice=%s, rate=%s)", len(text), voice, rate)
}

// synthesizeOnce executes a single edge-tts CLI invocation.
func (c *EdgeTTSClient) synthesizeOnce(ctx context.Context, text, voice, rate, pitch, outputPath string) error {
	cmd := exec.CommandContext(ctx, "edge-tts",
		"--text", text,
		"--voice", voice,
		"--rate", rate,
		"--pitch", pitch,
		"--write-media", outputPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		if stderrStr != "" {
			return fmt.Errorf("edge-tts exec failed: %w, stderr: %s", err, truncateString(stderrStr, 500))
		}
		return fmt.Errorf("edge-tts exec failed: %w", err)
	}

	return nil
}

// ── Package-level Helpers ────────────────────────────────────────────────────

// SynthesizeWithRetry is a convenience wrapper around EdgeTTSClient.Synthesize.
// It matches the retry pattern from the Python implementation
// (python-worker/utils/audio.py:_tts_single).
func SynthesizeWithRetry(ctx context.Context, client *EdgeTTSClient, text, voice, rate, pitch, outputPath string) error {
	return client.Synthesize(ctx, text, voice, rate, pitch, outputPath)
}

// EstimateSpeechDuration estimates the speech duration in seconds for a given
// text, based on characters-per-second. Ported from
// python-worker/utils/audio.py:estimate_speech_duration.
//
// The default rate of 15 chars/sec is calibrated for Vietnamese (vi-VN).
func EstimateSpeechDuration(text string, charsPerSecond float64) float64 {
	if charsPerSecond <= 0 {
		charsPerSecond = 15.0
	}
	return float64(len(text)) / charsPerSecond
}

// ComputeRateAndStretch computes the TTS speed rate and time-stretch ratio needed
// to fit speech into a target duration window.
//
// Two-tier strategy (ported from python-worker/utils/audio.py:compute_rate_for_duration):
//   Tier 1 — TTS speed-up (max maxSpeedupPercent): model-level, natural sounding.
//   Tier 2 — DSP time-stretch (max maxStretchPercent): phase vocoder, preserves pitch.
//   Fallback — if both tiers are insufficient, overflow=true is returned for the
//              caller to handle (cut + fade-out in AlignAudioToWindow).
//
// Parameters:
//   - text: the speech text
//   - targetDuration: target duration in seconds
//   - charsPerSecond: speech rate in characters per second
//   - maxSpeedupPercent: max allowed TTS speed-up percentage (e.g. 25 for 25%)
//   - maxStretchPercent: max allowed DSP stretch percentage (e.g. 15 for 15%)
//
// Returns:
//   - rate: TTS rate string (e.g. "+0%", "+20%", "+25%")
//   - stretchRatio: DSP stretch ratio (0.0 = none, >0 = stretch needed)
//   - overflow: true if the text cannot fit even with max speed-up and stretch
func ComputeRateAndStretch(text string, targetDuration float64, charsPerSecond float64, maxSpeedupPercent int, maxStretchPercent int) (rate string, stretchRatio float64, overflow bool) {
	naturalDuration := EstimateSpeechDuration(text, charsPerSecond)

	if naturalDuration <= targetDuration {
		return "+0%", 0.0, false
	}

	ratio := naturalDuration / targetDuration
	maxSpeedup := 1.0 + float64(maxSpeedupPercent)/100.0
	maxStretch := float64(maxStretchPercent) / 100.0

	// Tier 1: TTS speed-up alone sufficient?
	if ratio <= maxSpeedup {
		pct := int(math.Ceil((ratio - 1.0) * 100))
		if pct < 0 {
			pct = 0
		}
		return fmt.Sprintf("+%d%%", pct), 0.0, false
	}

	// Tier 2: max speed-up + time-stretch for remainder
	remaining := ratio / maxSpeedup
	stretchNeeded := remaining - 1.0

	if stretchNeeded <= maxStretch {
		return fmt.Sprintf("+%d%%", maxSpeedupPercent), stretchNeeded, false
	}

	// Overflow: beyond both thresholds
	return fmt.Sprintf("+%d%%", maxSpeedupPercent), 0.0, true
}

// ── Filters ──────────────────────────────────────────────────────────────────

var (
	rePureMusic     = regexp.MustCompile(`^[♪♫🎵🎶\[\]()\s]+$`)
	reBrackets      = regexp.MustCompile(`\[.*?\]`)
	reParens        = regexp.MustCompile(`\(.*?\)`)
	reMusicSymbols  = regexp.MustCompile(`[♪♫🎵🎶]`)
	reBoldMarkup    = regexp.MustCompile(`\*\*.*?\*\*`)
	reWhitespace    = regexp.MustCompile(`\s+`)
)

// FilterSpeechText removes non-speech symbols from text, matching the Python
// implementation (python-worker/utils/audio.py:filter_speech_text).
// Returns empty string if the text contains only music symbols.
func FilterSpeechText(text string) string {
	if text == "" {
		return ""
	}

	// Pure music-symbol lines produce no speech.
	if rePureMusic.MatchString(text) {
		return ""
	}

	// Remove [sound effects], [music], [applause], etc.
	text = reBrackets.ReplaceAllString(text, "")

	// Remove (laughs), (sighs), etc.
	text = reParens.ReplaceAllString(text, "")

	// Remove music symbols ♪♫🎵🎶.
	text = reMusicSymbols.ReplaceAllString(text, "")

	// Remove **bold markup**.
	text = reBoldMarkup.ReplaceAllString(text, "")

	// Collapse and trim whitespace.
	text = reWhitespace.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	return text
}

// ── Internal Helpers ─────────────────────────────────────────────────────────

func dirName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
