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
)

// DubMixer mixes video and audio streams using FFmpeg for the dubbing pipeline.
type DubMixer struct {
	ffmpegPath  string
	ffprobePath string
	// VideoEncoder is the video codec used for encoding output files.
	// Default "libopenh264" (H.264). Override for testing or custom codec needs.
	VideoEncoder string
}

// NewDubMixer creates a DubMixer with default settings (libopenh264 video encoder).
// Empty paths default to "ffmpeg" and "ffprobe" found on PATH.
func NewDubMixer() *DubMixer {
	return &DubMixer{
		ffmpegPath:   "ffmpeg",
		ffprobePath:  "ffprobe",
		VideoEncoder: "libopenh264",
	}
}

// MixVoiceover creates a voiceover-style video by mixing original audio (15%),
// narrative TTS (100%), and optional BGM (80%).
//
// Parameters:
//   - videoPath: path to the original video file
//   - narrativePath: path to the narrative TTS audio (required)
//   - bgmPath: path to background music (optional; pass "" to skip)
//   - outputPath: path for the output MP4 file
func (m *DubMixer) MixVoiceover(ctx context.Context, videoPath, narrativePath, bgmPath, outputPath string) error {
	return m.mix(ctx, videoPath, narrativePath, bgmPath, "", outputPath, "voiceover")
}

func (m *DubMixer) MixVoiceoverWithSubs(ctx context.Context, videoPath, narrativePath, bgmPath, subsPath, outputPath string) error {
	return m.mix(ctx, videoPath, narrativePath, bgmPath, subsPath, outputPath, "voiceover")
}

// MixReplacement creates a replacement-style video by mixing dubbing TTS (100%)
// and optional BGM (80%), with no original audio.
//
// Parameters:
//   - videoPath: path to the original video file
//   - dubPath: path to the dubbing TTS audio (required)
//   - bgmPath: path to background music (optional; pass "" to skip)
//   - outputPath: path for the output MP4 file
func (m *DubMixer) MixReplacement(ctx context.Context, videoPath, dubPath, bgmPath, outputPath string) error {
	return m.mix(ctx, videoPath, dubPath, bgmPath, "", outputPath, "replacement")
}

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

	var audioLabel string

	if bgmExists {
		args = append(args, "-i", bgmPath)
		switch mode {
		case "voiceover":
			audioFilter = "[1:a]volume=1.0[narr];[2:a]volume=0.8[bgm];[0:a]volume=0.15[orig];[narr][bgm][orig]amix=inputs=3:duration=shortest[audio]"
			audioLabel = "[audio]"
		case "replacement":
			audioFilter = "[1:a]volume=1.0[dub];[2:a]volume=0.8[bgm];[dub][bgm]amix=inputs=2:duration=shortest[audio]"
			audioLabel = "[audio]"
		}
	} else {
		switch mode {
		case "voiceover":
			audioFilter = "[1:a]volume=1.0[narr];[0:a]volume=0.15[orig];[narr][orig]amix=inputs=2:duration=shortest[audio]"
			audioLabel = "[audio]"
		case "replacement":
			audioFilter = "[1:a]volume=1.0[dub];[0:a]volume=0[orig];[dub][orig]amix=inputs=2:duration=shortest[audio]"
			audioLabel = "[audio]"
		}
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

	videoEncoder := m.VideoEncoder
	if videoEncoder == "" {
		videoEncoder = "libopenh264"
	}

	args = append(args,
		"-c:v", videoEncoder,
		"-crf", "23",
		"-preset", "medium",
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
