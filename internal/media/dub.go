// Package media provides media processing utilities using FFmpeg.
package media

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// DubMixer mixes video and audio streams using FFmpeg for the dubbing pipeline.
type DubMixer struct {
	ffmpegPath  string
	ffprobePath string
	// VideoEncoder is the H.264 encoder used when the video must be re-encoded
	// (to burn in subtitles). Empty means auto: libx264 when this FFmpeg has
	// it, otherwise libopenh264 (Fedora's FFmpeg ships without libx264).
	VideoEncoder string
}

// NewDubMixer creates a DubMixer that uses ffmpeg and ffprobe from PATH.
func NewDubMixer() *DubMixer {
	return &DubMixer{
		ffmpegPath:  "ffmpeg",
		ffprobePath: "ffprobe",
	}
}

var (
	detectOnce      sync.Once
	detectedEncoder string
)

// videoEncoder returns the configured encoder, or detects one once per process.
func (m *DubMixer) videoEncoder() string {
	if m.VideoEncoder != "" {
		return m.VideoEncoder
	}
	detectOnce.Do(func() {
		detectedEncoder = "libopenh264"
		out, err := exec.Command(m.ffmpegPath, "-hide_banner", "-encoders").Output()
		if err == nil && strings.Contains(string(out), " libx264 ") {
			detectedEncoder = "libx264"
		}
	})
	return detectedEncoder
}

// videoCodecArgs returns the video codec options. Each encoder takes its own
// quality settings: libopenh264 ignores -crf and -preset, so without an
// explicit bitrate it falls back to its low default.
func videoCodecArgs(encoder string, reencode bool) []string {
	if !reencode {
		return []string{"-c:v", "copy"} // nothing to draw on the picture
	}
	switch encoder {
	case "libx264":
		return []string{"-c:v", "libx264", "-crf", "23", "-preset", "medium", "-pix_fmt", "yuv420p"}
	case "libopenh264":
		// Downloads are capped at 720p (see stepDownload); 2.5 Mb/s suits that.
		return []string{"-c:v", "libopenh264", "-b:v", "2500k", "-pix_fmt", "yuv420p"}
	default:
		return []string{"-c:v", encoder}
	}
}

// MixVoiceoverWithSubs creates a voiceover-style video by mixing original
// audio (15%), narrative TTS (100%) and optional BGM (80%), with optional
// burned-in subtitles.
func (m *DubMixer) MixVoiceoverWithSubs(ctx context.Context, videoPath, narrativePath, bgmPath, subsPath, outputPath string) error {
	return m.mix(ctx, videoPath, narrativePath, bgmPath, subsPath, outputPath, "voiceover")
}

// MixReplacementWithSubs creates a replacement-style video with the dubbing
// TTS (100%) and optional BGM (80%) instead of the original audio, with
// optional burned-in subtitles.
func (m *DubMixer) MixReplacementWithSubs(ctx context.Context, videoPath, dubPath, bgmPath, subsPath, outputPath string) error {
	return m.mix(ctx, videoPath, dubPath, bgmPath, subsPath, outputPath, "replacement")
}

// mix builds and executes the FFmpeg command for the given mix mode.
func (m *DubMixer) mix(ctx context.Context, videoPath, audioPath, bgmPath, subsPath, outputPath, mode string) error {
	// Verify required inputs exist.
	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Errorf("video file not found at %s: %w", videoPath, err)
	}
	if _, err := os.Stat(audioPath); err != nil {
		return fmt.Errorf("audio file not found at %s: %w", audioPath, err)
	}

	// Check if BGM exists (optional).
	bgmExists := false
	if bgmPath != "" {
		if _, err := os.Stat(bgmPath); err == nil {
			bgmExists = true
		}
	}

	// Check if subtitle exists (optional).
	subsExists := subsPath != "" && fileExists(subsPath)

	// Build FFmpeg arguments.
	args := m.buildArgs(videoPath, audioPath, bgmPath, subsPath, outputPath, mode, bgmExists, subsExists)

	// Ensure output directory exists.
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outputDir, err)
	}

	// Execute FFmpeg.
	cmd := exec.CommandContext(ctx, m.ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg %s mix failed: %w, output: %s", mode, err, string(output))
	}

	// Verify output file exists and has content.
	fi, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("output file not found after ffmpeg %s mix: %w", mode, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("output file %s is empty (0 bytes) after %s mix", outputPath, mode)
	}

	return nil
}

// buildArgs constructs the FFmpeg argument list for the given mix mode.
func (m *DubMixer) buildArgs(videoPath, audioPath, bgmPath, subsPath, outputPath, mode string, bgmExists, subsExists bool) []string {
	args := []string{
		"-i", videoPath,
		"-i", audioPath,
	}

	var audioFilter, videoFilter, videoLabel string

	// Audio graph rules:
	//   - amix normalize=0 keeps each input at its own volume. The default
	//     divides every input by the number of inputs, halving the TTS voice.
	//   - The graph ends with apad (endless silence) and "-shortest" is set
	//     below, so the output always lasts exactly as long as the video.
	//     An audio track shorter than the video no longer truncates it.
	//   - Replacement mode never reads [0:a], so it also works on videos
	//     without an audio stream.
	audioLabel := "[audio]"
	switch {
	case mode == "voiceover" && bgmExists:
		args = append(args, "-i", bgmPath)
		audioFilter = "[0:a]volume=0.15[orig];[1:a]volume=1.0[narr];[2:a]volume=0.8[bgm];" +
			"[orig][narr][bgm]amix=inputs=3:duration=longest:normalize=0,apad[audio]"
	case mode == "voiceover":
		audioFilter = "[0:a]volume=0.15[orig];[1:a]volume=1.0[narr];" +
			"[orig][narr]amix=inputs=2:duration=longest:normalize=0,apad[audio]"
	case bgmExists: // replacement
		args = append(args, "-i", bgmPath)
		audioFilter = "[1:a]volume=1.0[dub];[2:a]volume=0.8[bgm];" +
			"[dub][bgm]amix=inputs=2:duration=longest:normalize=0,apad[audio]"
	default: // replacement
		audioFilter = "[1:a]volume=1.0,apad[audio]"
	}

	if subsExists {
		subsAbs, _ := filepath.Abs(subsPath)
		videoFilter = fmt.Sprintf(
			`subtitles='%s':force_style='FontName=Arial,FontSize=20,PrimaryColour=&H00FFFFFF,Outline=2,Shadow=2'`,
			strings.ReplaceAll(subsAbs, "\\", "/"),
		)
		videoLabel = "[vout]"
	} else {
		videoLabel = "0:v:0"
	}

	var filterComplex string
	if videoFilter != "" {
		filterComplex = fmt.Sprintf("[0:v]%s%s;%s", videoFilter, videoLabel, audioFilter)
	} else {
		filterComplex = audioFilter
	}

	args = append(args, "-filter_complex", filterComplex)

	if videoFilter != "" {
		args = append(args, "-map", "[vout]", "-map", audioLabel)
	} else {
		args = append(args, "-map", videoLabel, "-map", audioLabel)
	}

	args = append(args, videoCodecArgs(m.videoEncoder(), videoFilter != "")...)
	args = append(args,
		"-c:a", "aac",
		"-b:a", "192k",
		"-movflags", "+faststart",
		"-shortest",
		"-y",
		outputPath,
	)

	return args
}

// VerifyOutput checks that the output video file exists, has non-zero size,
// and its duration is within 5% of the expected duration.
func (m *DubMixer) VerifyOutput(outputPath string, expectedDuration float64) error {
	// Check file exists.
	fi, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("output file not found at %s: %w", outputPath, err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("output file %s is empty (0 bytes)", outputPath)
	}
	if expectedDuration <= 0 {
		// No expected duration to compare against — just check file exists.
		return nil
	}

	// Get actual duration.
	actualDuration, err := GetVideoDuration(outputPath, m.ffprobePath)
	if err != nil {
		return fmt.Errorf("get duration of %s: %w", outputPath, err)
	}
	if actualDuration <= 0 {
		return fmt.Errorf("output file %s has invalid duration: %f", outputPath, actualDuration)
	}

	// Allow ±5% tolerance.
	tolerance := expectedDuration * 0.05
	diff := math.Abs(actualDuration - expectedDuration)
	if diff > tolerance {
		return fmt.Errorf(
			"output duration mismatch: expected %.2fs, got %.2fs (diff %.2fs, tolerance %.2fs, %.1f%%)",
			expectedDuration, actualDuration, diff, tolerance, (diff/expectedDuration)*100,
		)
	}

	return nil
}

// GetVideoDuration returns the video duration in seconds using ffprobe.
func GetVideoDuration(filePath, ffprobePath string) (float64, error) {
	if ffprobePath == "" {
		ffprobePath = "ffprobe"
	}

	cmd := exec.Command(ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	durationStr := strings.TrimSpace(string(output))
	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ffprobe duration %q: %w", durationStr, err)
	}

	return duration, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
