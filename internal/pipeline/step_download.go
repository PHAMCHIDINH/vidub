package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func (p *Pipeline) stepDownload(ctx context.Context, job *VideoJob) error {
	outputPath := filepath.Join(job.WorkDir, job.VideoID+".mp4")
	if fileExists(outputPath) && !job.ForceRefresh["download"] {
		log.Printf("[%s] download skipped (video exists)", job.VideoID)
		return nil
	}
	if job.ForceRefresh["download"] && fileExists(outputPath) {
		log.Printf("[%s] download: force refresh, deleting cached video", job.VideoID)
		os.Remove(outputPath)
	}

	log.Printf("[%s] downloading video with yt-dlp (max 720p)...", job.VideoID)

	// Built from the validated video ID, never from user input, and placed
	// after "--" so yt-dlp can never read it as an option.
	url := "https://www.youtube.com/watch?v=" + job.VideoID

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"-f", "bestvideo[height<=720]+bestaudio/best[height<=720]/best",
		"--merge-output-format", "mp4",
		"-o", outputPath,
		"--no-playlist",
		"--quiet",
		"--",
		url,
	)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	if !fileExists(outputPath) {
		return fmt.Errorf("download succeeded but file not found")
	}
	return nil
}
