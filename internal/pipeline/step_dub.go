package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"vidub/internal/media"
	"vidub/internal/tts"
)

func (p *Pipeline) stepDub(ctx context.Context, job *VideoJob) error {
	videoPath := filepath.Join(job.WorkDir, job.VideoID+".mp4")
	narrativePath := filepath.Join(job.WorkDir, "narrative.mp3")
	dubPath := filepath.Join(job.WorkDir, "dub.mp3")
	voiceoverPath := filepath.Join(job.WorkDir, "voiceover.mp4")
	dubVideoPath := filepath.Join(job.WorkDir, "dub.mp4")
	subtitlePath := filepath.Join(job.WorkDir, "subtitle.srt")

	mixer := media.NewDubMixer()

	translatedPath := filepath.Join(job.WorkDir, "translated.json")
	subsAvailable := false
	if !fileExists(subtitlePath) && fileExists(translatedPath) {
		if err := generateSubtitle(translatedPath, subtitlePath); err != nil {
			log.Printf("[%s] subtitle generation failed: %v", job.VideoID, err)
		} else {
			log.Printf("[%s] subtitle generated: %s", job.VideoID, subtitlePath)
			subsAvailable = true
		}
	} else if fileExists(subtitlePath) {
		subsAvailable = true
	}

	hasNarrative := fileExists(narrativePath)
	hasDub := fileExists(dubPath)

	// Prepare: get video duration and align audio if needed.
	var videoDur float64
	if hasNarrative || hasDub {
		var err error
		videoDur, err = media.GetVideoDuration(videoPath, "")
		if err != nil {
			return fmt.Errorf("get video duration: %w", err)
		}
	}

	alignedNarrative := narrativePath
	if hasNarrative {
		alignedNarrative = alignAudioToVideo(job, narrativePath, videoPath, videoDur)
	}

	alignedDub := dubPath
	if hasDub {
		alignedDub = alignAudioToVideo(job, dubPath, videoPath, videoDur)
	}

	subs := ""
	if subsAvailable {
		subs = subtitlePath
	}

	// Run voiceover and replacement concurrently (both ffmpeg, independent I/O).
	var wg sync.WaitGroup
	var voiceoverErr, replacementErr error
	wg.Add(2)

	if hasNarrative {
		go func() {
			defer wg.Done()
			log.Printf("[%s] dubbing: mixing voiceover...", job.VideoID)
			if err := mixer.MixVoiceoverWithSubs(ctx, videoPath, alignedNarrative, "", subs, voiceoverPath); err != nil {
				voiceoverErr = fmt.Errorf("voiceover mix: %w", err)
				return
			}
			if err := mixer.VerifyOutput(voiceoverPath, videoDur); err != nil {
				voiceoverErr = fmt.Errorf("voiceover verify: %w", err)
				return
			}
			log.Printf("[%s] dubbing: voiceover done (%.1fs)", job.VideoID, videoDur)
		}()
	} else {
		wg.Done()
	}

	if hasDub {
		go func() {
			defer wg.Done()
			log.Printf("[%s] dubbing: mixing replacement...", job.VideoID)
			if err := mixer.MixReplacementWithSubs(ctx, videoPath, alignedDub, "", subs, dubVideoPath); err != nil {
				replacementErr = fmt.Errorf("replacement mix: %w", err)
				return
			}
			if err := mixer.VerifyOutput(dubVideoPath, videoDur); err != nil {
				replacementErr = fmt.Errorf("replacement verify: %w", err)
				return
			}
			log.Printf("[%s] dubbing: replacement done (%.1fs)", job.VideoID, videoDur)
		}()
	} else {
		wg.Done()
	}

	wg.Wait()

	if voiceoverErr != nil {
		return voiceoverErr
	}
	if replacementErr != nil {
		return replacementErr
	}

	return nil
}

func generateSubtitle(translatedPath, outputPath string) error {
	data, err := os.ReadFile(translatedPath)
	if err != nil {
		return fmt.Errorf("read translated: %w", err)
	}

	var doc struct {
		Segments []struct {
			Text     string  `json:"text"`
			Start    float64 `json:"start"`
			Duration float64 `json:"duration"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse translated: %w", err)
	}

	segments := make([]media.SubtitleSegment, len(doc.Segments))
	for i, s := range doc.Segments {
		segments[i] = media.SubtitleSegment{
			Text:     s.Text,
			Start:    s.Start,
			Duration: s.Duration,
		}
	}

	return media.GenerateSRT(segments, outputPath)
}

func alignAudioToVideo(job *VideoJob, audioPath, videoPath string, videoDur float64) string {
	dur, err := media.GetVideoDuration(audioPath, "")
	if err != nil || dur <= videoDur {
		return audioPath
	}
	log.Printf("[%s] dubbing: %s (%.1fs) longer than video (%.1fs), aligning...",
		job.VideoID, filepath.Base(audioPath), dur, videoDur)
	aligned, err := tts.AlignAudioToWindow(audioPath, videoDur, job.WorkDir)
	if err != nil {
		log.Printf("[%s] align %s failed, using original: %v", job.VideoID, filepath.Base(audioPath), err)
		return audioPath
	}
	alignedDur, _ := media.GetVideoDuration(aligned, "")
	log.Printf("[%s] dubbing: %s aligned to %.1fs", job.VideoID, filepath.Base(audioPath), alignedDur)
	return aligned
}
