package pipeline

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// stepTTS builds the speech track of the job's Mix Mode:
// narrative.mp3 (continuous reading) for voiceover, dub.mp3 (each line at
// its timestamp) for replace.
func (p *Pipeline) stepTTS(ctx context.Context, job *VideoJob) error {
	translatedPath := filepath.Join(job.WorkDir, "translated.json")
	scriptMode, outPath := "narrative", filepath.Join(job.WorkDir, "narrative.mp3")
	if job.Mode == ModeReplace {
		scriptMode, outPath = "dub", filepath.Join(job.WorkDir, "dub.mp3")
	}
	name := filepath.Base(outPath)

	if !job.ForceRefresh["tts"] && fresh(outPath, translatedPath) {
		log.Printf("[%s] tts skipped (%s is up to date)", job.VideoID, name)
		return nil
	}

	voice := job.Voice
	if voice == "" {
		voice = p.cfg.TTSVoice
	}

	log.Printf("[%s] tts: generating %s...", job.VideoID, name)
	script := filepath.Join("scripts", "tts.py")
	cmd := exec.CommandContext(ctx, "python3", script, job.VideoID, job.WorkDir, voice, "--mode", scriptMode)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s tts failed: %w", scriptMode, err)
	}
	if !fileExists(outPath) {
		return fmt.Errorf("%s tts finished but %s was not written", scriptMode, name)
	}
	return nil
}
