package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"vidub/internal/media"
	"vidub/internal/tts"
)

// stepDub mixes the speech track of the job's Mix Mode into the video and
// burns in the subtitles: voiceover.mp4 for voiceover, dub.mp4 for replace.
func (p *Pipeline) stepDub(ctx context.Context, job *VideoJob) error {
	videoPath := filepath.Join(job.WorkDir, job.VideoID+".mp4")
	translatedPath := filepath.Join(job.WorkDir, "translated.json")
	subtitlePath := filepath.Join(job.WorkDir, "subtitle.srt")

	audioPath, outPath := filepath.Join(job.WorkDir, "narrative.mp3"), filepath.Join(job.WorkDir, "voiceover.mp4")
	if job.Mode == ModeReplace {
		audioPath, outPath = filepath.Join(job.WorkDir, "dub.mp3"), filepath.Join(job.WorkDir, "dub.mp4")
	}
	force := job.ForceRefresh["dub"]

	// Subtitles come from translated.json, so rebuild them when it changed.
	if force || !fresh(subtitlePath, translatedPath) {
		if err := generateSubtitle(translatedPath, subtitlePath); err != nil {
			log.Printf("[%s] subtitle generation failed, continuing without subtitles: %v", job.VideoID, err)
			os.Remove(subtitlePath) // never burn in subtitles from an older translation
		} else {
			log.Printf("[%s] subtitle generated: %s", job.VideoID, subtitlePath)
		}
	}
	subs := ""
	if fileExists(subtitlePath) {
		subs = subtitlePath
	}

	if !force && fresh(outPath, videoPath, audioPath, subtitlePath) {
		log.Printf("[%s] dub skipped (%s is up to date)", job.VideoID, filepath.Base(outPath))
		return nil
	}
	if !fileExists(audioPath) {
		return fmt.Errorf("%s not found; the tts step did not produce it", filepath.Base(audioPath))
	}

	videoDur, err := media.GetVideoDuration(videoPath, "")
	if err != nil {
		return fmt.Errorf("get video duration: %w", err)
	}

	// Mix into a temporary file and rename at the end, so an interrupted mix
	// never leaves a broken video that a later run would take as up to date.
	tmpOut := strings.TrimSuffix(outPath, ".mp4") + ".tmp.mp4"
	defer os.Remove(tmpOut)

	mixer := media.NewDubMixer()
	log.Printf("[%s] dubbing: mixing %s...", job.VideoID, job.Mode)
	if job.Mode == ModeReplace {
		// dub.mp3 is never time-scaled: every segment already sits at its own
		// timestamp, so speeding up the whole track would push speech out of
		// sync. The mixer pads or cuts it to the video length.
		err = mixer.MixReplacementWithSubs(ctx, videoPath, audioPath, "", subs, tmpOut)
	} else {
		aligned := alignAudioToVideo(job, audioPath, videoDur)
		if aligned != audioPath {
			defer os.Remove(aligned)
		}
		err = mixer.MixVoiceoverWithSubs(ctx, videoPath, aligned, "", subs, tmpOut)
	}
	if err != nil {
		return fmt.Errorf("%s mix: %w", job.Mode, err)
	}
	if err := mixer.VerifyOutput(tmpOut, videoDur); err != nil {
		return fmt.Errorf("%s verify: %w", job.Mode, err)
	}
	if err := os.Rename(tmpOut, outPath); err != nil {
		return fmt.Errorf("save %s: %w", filepath.Base(outPath), err)
	}
	log.Printf("[%s] dubbing: %s done (%.1fs)", job.VideoID, filepath.Base(outPath), videoDur)
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

func alignAudioToVideo(job *VideoJob, audioPath string, videoDur float64) string {
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
