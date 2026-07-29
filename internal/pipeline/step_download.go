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
	if fileExists(outputPath) {
		log.Printf("[%s] download skipped (video exists)", job.VideoID)
		return nil
	}

	log.Printf("[%s] downloading video with yt-dlp (max 720p)...", job.VideoID)

	url := job.URL
	if url == "" {
		url = "https://www.youtube.com/watch?v=" + job.VideoID
	}

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"-f", "bestvideo[height<=720]+bestaudio/best[height<=720]/best",
		"--merge-output-format", "mp4",
		"-o", outputPath,
		"--no-playlist",
		"--no-check-certificates",
		"--quiet",
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
