package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

func (p *Pipeline) stepTTS(ctx context.Context, job *VideoJob) error {
	narrativePath := filepath.Join(job.WorkDir, "narrative.mp3")
	dubPath := filepath.Join(job.WorkDir, "dub.mp3")

	hasNarrative := fileExists(narrativePath)
	hasDub := fileExists(dubPath)

	if hasNarrative && hasDub {
		log.Printf("[%s] tts skipped (both narrative and dub exist)", job.VideoID)
		return nil
	}

	voice := job.Voice
	if voice == "" {
		voice = p.cfg.TTSVoice
	}

	script := filepath.Join("scripts", "tts.py")

	// Run narrative and dub TTS concurrently (Edge-TTS network calls, independent).
	var wg sync.WaitGroup
	var narrativeErr, dubErr error
	wg.Add(2)

	if !hasNarrative {
		go func() {
			defer wg.Done()
			log.Printf("[%s] tts: generating narrative...", job.VideoID)
			cmd := exec.CommandContext(ctx, "python3", script, job.VideoID, job.WorkDir, voice, "--mode", "narrative")
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				narrativeErr = fmt.Errorf("narrative tts failed: %w", err)
			}
		}()
	} else {
		wg.Done()
	}

	if !hasDub {
		go func() {
			defer wg.Done()
			log.Printf("[%s] tts: generating dubbing...", job.VideoID)
			cmd := exec.CommandContext(ctx, "python3", script, job.VideoID, job.WorkDir, voice, "--mode", "dub")
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				dubErr = fmt.Errorf("dub tts failed: %w", err)
			}
		}()
	} else {
		wg.Done()
	}

	wg.Wait()

	if narrativeErr != nil {
		return narrativeErr
	}
	if dubErr != nil {
		return dubErr
	}

	log.Printf("[%s] tts: both completed", job.VideoID)
	return nil
}
