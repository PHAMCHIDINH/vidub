package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func (p *Pipeline) stepExtract(ctx context.Context, job *VideoJob) error {
	outputPath := filepath.Join(job.WorkDir, "transcript.json")
	if fileExists(outputPath) {
		log.Printf("[%s] extract skipped (transcript.json exists)", job.VideoID)
		return nil // already done
	}

	log.Printf("[%s] extracting transcript from YouTube...", job.VideoID)
	script := filepath.Join("scripts", "extract.py")
	cmd := exec.CommandContext(ctx, "python3", script, job.VideoID, job.URL, job.WorkDir)
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	if !fileExists(outputPath) {
		return fmt.Errorf("extract succeeded but transcript.json not found")
	}
	return nil
}
