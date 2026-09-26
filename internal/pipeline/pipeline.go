package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"vidub/internal/config"
	"vidub/internal/sse"
	"vidub/internal/storage"
	"vidub/internal/store"
)

// Mix Modes. A job only builds the audio and video of its mode.
const (
	ModeVoiceover = "voiceover" // narrative.mp3 over the original audio: voiceover.mp4
	ModeReplace   = "replace"   // timestamped dub.mp3 instead of it: dub.mp4
)

type VideoJob struct {
	VideoID string
	URL     string
	Mode    string
	Voice   string
	APIKey  string
	WorkDir string
	// ForceRefresh maps a step name to true when its cached output must be recomputed.
	ForceRefresh map[string]bool
}

type JobResult struct {
	VideoID      string
	VoiceoverMP4 string
	DubMP4       string
}

type Pipeline struct {
	cfg    *config.Config
	broker *sse.Broker
	jobs   *store.JobStore
}

type stepFunc func(context.Context, *VideoJob) error

type stepEntry struct {
	name string
	fn   stepFunc
}

// totalSteps is used for the progress percentage.
const totalSteps = 5

func New(cfg *config.Config, broker *sse.Broker, jobs *store.JobStore) *Pipeline {
	return &Pipeline{cfg: cfg, broker: broker, jobs: jobs}
}

// Run executes the whole pipeline and records the final JobStatus in the store.
func (p *Pipeline) Run(ctx context.Context, job VideoJob) (*JobResult, error) {
	log.Printf("[%s] 🚀 pipeline started | mode=%s | voice=%s", job.VideoID, job.Mode, job.Voice)

	if err := p.run(ctx, &job); err != nil {
		// Status first, so a page reload after the event renders the final state.
		p.finish(&job, store.JobFailed)
		p.emitError(job.VideoID, err.Error())
		return nil, err
	}

	p.finish(&job, store.JobCompleted)
	p.emitComplete(job.VideoID, job.WorkDir)
	log.Printf("[%s] ✅ pipeline finished successfully", job.VideoID)

	return &JobResult{
		VideoID:      job.VideoID,
		VoiceoverMP4: filepath.Join(job.WorkDir, "voiceover.mp4"),
		DubMP4:       filepath.Join(job.WorkDir, "dub.mp4"),
	}, nil
}

// finish records the final status in the store and in job.json.
func (p *Pipeline) finish(job *VideoJob, status store.JobStatus) {
	if stored, ok := p.jobs.SetStatus(job.VideoID, status); ok {
		p.saveMeta(job.WorkDir, stored)
	}
}

func (p *Pipeline) saveMeta(dir string, stored store.StoredJob) {
	if err := storage.SaveJobMeta(dir, stored); err != nil {
		log.Printf("[%s] cannot save job.json: %v", stored.VideoID, err)
	}
}

func (p *Pipeline) run(ctx context.Context, job *VideoJob) error {
	if err := ensureDir(job.WorkDir); err != nil {
		return fmt.Errorf("cannot create work dir: %w", err)
	}
	// Cleanup ages jobs by directory mtime. A re-run served entirely from
	// cache writes nothing new, so refresh it to count from this run.
	now := time.Now()
	if err := os.Chtimes(job.WorkDir, now, now); err != nil {
		log.Printf("[%s] cannot refresh work dir time: %v", job.VideoID, err)
	}
	if stored, ok := p.jobs.Get(job.VideoID); ok {
		p.saveMeta(job.WorkDir, stored)
	}

	var completed atomic.Int32
	step := func(name string, fn stepFunc) error {
		if err := p.runStep(ctx, job, name, fn); err != nil {
			return err
		}
		pct := float64(completed.Add(1)) / totalSteps * 100
		p.emitProgress(job.VideoID, pct)
		return nil
	}

	// extract and download are independent network I/O, so run them together.
	var wg sync.WaitGroup
	var extractErr, downloadErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		extractErr = step("extract", p.stepExtract)
	}()
	go func() {
		defer wg.Done()
		downloadErr = step("download", p.stepDownload)
	}()
	wg.Wait()
	if err := errors.Join(extractErr, downloadErr); err != nil {
		return err
	}

	// The rest is sequential: each step reads the previous step's output.
	for _, s := range []stepEntry{
		{"translate", p.stepTranslate},
		{"tts", p.stepTTS},
		{"dub", p.stepDub},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := step(s.name, s.fn); err != nil {
			return err
		}
	}
	return nil
}

// runStep runs one step and reports it to the log and the timeline.
func (p *Pipeline) runStep(ctx context.Context, job *VideoJob, name string, fn stepFunc) error {
	log.Printf("[%s] ▶ %s starting...", job.VideoID, name)
	p.emitStepLog(job.VideoID, name, "started", name+" starting...", 0)

	start := time.Now()
	err := fn(ctx, job)
	elapsed := time.Since(start).Seconds()

	if err != nil {
		log.Printf("[%s] ✗ %s FAILED (%.1fs): %v", job.VideoID, name, elapsed, err)
		p.emitStepLog(job.VideoID, name, "failed", err.Error(), elapsed)
		return fmt.Errorf("step %s: %w", name, err)
	}

	log.Printf("[%s] ✓ %s completed (%.1fs)", job.VideoID, name, elapsed)
	p.emitStepLog(job.VideoID, name, "completed", fmt.Sprintf("Done in %.1fs", elapsed), elapsed)
	return nil
}
