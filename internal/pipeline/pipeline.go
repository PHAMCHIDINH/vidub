package pipeline

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"vidub/internal/config"
	"vidub/internal/sse"
	"vidub/internal/storage"
)

type VideoJob struct {
	VideoID string
	URL     string
	Mode    string
	Voice   string
	APIKey  string
	WorkDir string
}

type JobResult struct {
	VideoID      string
	VoiceoverMP4 string
	DubMP4       string
	VoiceoverGDrive string
	DubGDrive       string
	SubtitleGDrive  string
	DriveUploaded   bool
}

type Pipeline struct {
	cfg    *config.Config
	broker *sse.Broker
	gdrive *storage.DriveClient
}

type stepEntry struct {
	name string
	fn   func(context.Context, *VideoJob) error
}

func New(cfg *config.Config, broker *sse.Broker, gdrive *storage.DriveClient) *Pipeline {
	return &Pipeline{cfg: cfg, broker: broker, gdrive: gdrive}
}

func (p *Pipeline) Run(ctx context.Context, job VideoJob) (*JobResult, error) {
	log.Printf("[%s] 🚀 pipeline started | mode=%s | voice=%s", job.VideoID, job.Mode, job.Voice)

	if err := ensureDir(job.WorkDir); err != nil {
		p.emitError(job.VideoID, "job_error", fmt.Sprintf("Cannot create work dir: %v", err))
		return nil, err
	}

	// Step 1: Run extract and download concurrently (both network I/O, independent).
	p.emitStepLog(job.VideoID, "extract", "started", "extract + download starting...", 0)
	p.emitStepLog(job.VideoID, "download", "started", "download starting...", 0)

	ioStart := time.Now()
	var wgIO sync.WaitGroup
	var extractErr, downloadErr error
	wgIO.Add(2)

	go func() {
		defer wgIO.Done()
		extractErr = p.stepExtract(ctx, &job)
	}()
	go func() {
		defer wgIO.Done()
		downloadErr = p.stepDownload(ctx, &job)
	}()

	wgIO.Wait()
	ioElapsed := time.Since(ioStart).Seconds()

	if extractErr != nil {
		log.Printf("[%s] ✗ extract FAILED (%.1fs): %v", job.VideoID, ioElapsed, extractErr)
		p.emitStepLog(job.VideoID, "extract", "failed", extractErr.Error(), ioElapsed)
		return nil, fmt.Errorf("step extract: %w", extractErr)
	}
	p.emitStepLog(job.VideoID, "extract", "completed", fmt.Sprintf("Done in %.1fs", ioElapsed), ioElapsed)
	p.emitProgress(job.VideoID, "extract", 20, nil)

	if downloadErr != nil {
		log.Printf("[%s] ✗ download FAILED (%.1fs): %v", job.VideoID, ioElapsed, downloadErr)
		p.emitStepLog(job.VideoID, "download", "failed", downloadErr.Error(), ioElapsed)
		return nil, fmt.Errorf("step download: %w", downloadErr)
	}
	p.emitStepLog(job.VideoID, "download", "completed", fmt.Sprintf("Done in %.1fs", ioElapsed), ioElapsed)
	p.emitProgress(job.VideoID, "download", 40, nil)

	// Steps 2-4: translate → tts → dub (sequential, each depends on previous).
	remainingSteps := []stepEntry{
		{"translate", p.stepTranslate},
		{"tts", p.stepTTS},
		{"dub", p.stepDub},
	}

	for i, s := range remainingSteps {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		stepNum := fmt.Sprintf("%d/%d", i+3, len(remainingSteps)+2)
		log.Printf("[%s] [%s] ▶ %s starting...", job.VideoID, stepNum, s.name)
		p.emitStepLog(job.VideoID, s.name, "started", s.name+" starting...", 0)

		startTime := time.Now()
		if err := s.fn(ctx, &job); err != nil {
			elapsed := time.Since(startTime).Seconds()
			log.Printf("[%s] [%s] ✗ %s FAILED (%.1fs): %v", job.VideoID, stepNum, s.name, elapsed, err)
			p.emitStepLog(job.VideoID, s.name, "failed", err.Error(), elapsed)
			p.emitError(job.VideoID, "job_error", fmt.Sprintf("Step %s failed: %v", s.name, err))
			return nil, fmt.Errorf("step %s: %w", s.name, err)
		}

		elapsed := time.Since(startTime).Seconds()
		log.Printf("[%s] [%s] ✓ %s completed (%.1fs)", job.VideoID, stepNum, s.name, elapsed)
		p.emitStepLog(job.VideoID, s.name, "completed", fmt.Sprintf("Done in %.1fs", elapsed), elapsed)

		pct := float64(i+3) / float64(len(remainingSteps)+2) * 100
		p.emitProgress(job.VideoID, s.name, pct, nil)
	}

	result := &JobResult{
		VideoID:      job.VideoID,
		VoiceoverMP4: filepath.Join(job.WorkDir, "voiceover.mp4"),
		DubMP4:       filepath.Join(job.WorkDir, "dub.mp4"),
	}

	// Upload to Google Drive if enabled.
	if p.gdrive != nil {
		p.emitStepLog(job.VideoID, "upload", "started", "uploading to Google Drive...", 0)
		uploadStart := time.Now()

		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer wg.Done()
			link, err := p.gdrive.UploadFile(result.VoiceoverMP4, job.VideoID, "video/mp4")
			if err != nil {
				log.Printf("[%s] gdrive voiceover upload failed: %v", job.VideoID, err)
				return
			}
			result.VoiceoverGDrive = link
			result.DriveUploaded = true
		}()

		go func() {
			defer wg.Done()
			link, err := p.gdrive.UploadFile(result.DubMP4, job.VideoID, "video/mp4")
			if err != nil {
				log.Printf("[%s] gdrive dub upload failed: %v", job.VideoID, err)
				return
			}
			result.DubGDrive = link
			result.DriveUploaded = true
		}()

		go func() {
			defer wg.Done()
			subtitlePath := filepath.Join(job.WorkDir, "subtitle.srt")
			link, err := p.gdrive.UploadFile(subtitlePath, job.VideoID, "text/plain")
			if err != nil {
				log.Printf("[%s] gdrive subtitle upload failed: %v", job.VideoID, err)
				return
			}
			result.SubtitleGDrive = link
			result.DriveUploaded = true
		}()

		wg.Wait()
		uploadElapsed := time.Since(uploadStart).Seconds()
		p.emitStepLog(job.VideoID, "upload", "completed", fmt.Sprintf("Uploaded in %.1fs", uploadElapsed), uploadElapsed)
	}

	p.emitComplete(job.VideoID, result)
	log.Printf("[%s] ✅ pipeline finished successfully", job.VideoID)
	return result, nil
}
