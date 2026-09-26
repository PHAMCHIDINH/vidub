package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"vidub/internal/translator"
)

// translateConcurrency is the number of chunks translated at the same time.
const translateConcurrency = 5

func (p *Pipeline) stepTranslate(ctx context.Context, job *VideoJob) error {
	inputPath := filepath.Join(job.WorkDir, "transcript.json")
	outputPath := filepath.Join(job.WorkDir, "translated.json")
	if !job.ForceRefresh["translate"] && fresh(outputPath, inputPath) {
		log.Printf("[%s] translate skipped (translated.json is up to date)", job.VideoID)
		return nil
	}
	if job.ForceRefresh["translate"] && fileExists(outputPath) {
		log.Printf("[%s] translate: force refresh, deleting cached translated.json", job.VideoID)
		os.Remove(outputPath)
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}

	var transcript struct {
		VideoID  string `json:"video_id"`
		Language string `json:"language"`
		Segments []struct {
			Text     string  `json:"text"`
			Start    float64 `json:"start"`
			Duration float64 `json:"duration"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal(data, &transcript); err != nil {
		return fmt.Errorf("parse transcript: %w", err)
	}

	segs := make([]translator.Segment, len(transcript.Segments))
	for i, s := range transcript.Segments {
		// YouTube caption text contains line breaks of its own. Flatten them:
		// they confuse the translator and end up in subtitles and TTS.
		segs[i] = translator.Segment{Text: strings.Join(strings.Fields(s.Text), " "), Start: s.Start, Duration: s.Duration}
	}

	// Tell the translator how long each line may be to be spoken in time.
	translator.SetCharBudgets(segs)

	apiKey := job.APIKey
	if apiKey == "" {
		apiKey = p.cfg.DeepSeekAPIKey
	}

	// DeepSeek is primary when a key is set, with Google as the fallback for
	// chunks DeepSeek cannot translate. Without a key, Google is all we have.
	var primary, fallback translator.TranslationProvider
	var providerName string
	if apiKey != "" {
		primary = translator.NewDeepSeekProvider(apiKey, p.cfg.DeepSeekModel)
		fallback = translator.NewGoogleProvider("vi")
		providerName = "deepseek"
	} else {
		primary = translator.NewGoogleProvider("vi")
		providerName = "google"
	}

	chunks := translator.ChunkTranscript(segs, translator.DefaultTargetWords, translator.DefaultMaxWords, translator.DefaultMaxDurationSecs)
	for i := range chunks {
		chunks[i].Context = translator.BuildContextWindow(chunks, i, translator.DefaultContextLines)
	}
	log.Printf("[%s] translating %d chunks in parallel (provider=%s)...", job.VideoID, len(chunks), providerName)

	outcome, err := translateAll(ctx, job.VideoID, chunks, primary, fallback)
	if err != nil {
		if translator.IsPermanent(err) {
			return fmt.Errorf("%s rejected the request (check the API key and account balance): %w", providerName, err)
		}
		return fmt.Errorf("translation failed, nothing was saved: %w", err)
	}

	if outcome.FallbackChunks > 0 {
		msg := fmt.Sprintf("%d/%d chunks were translated with Google Translate because %s failed: %v",
			outcome.FallbackChunks, len(chunks), providerName, outcome.FallbackReason)
		log.Printf("[%s] ⚠ %s", job.VideoID, msg)
		p.emitStepLog(job.VideoID, "translate", "warning", msg, 0)
	}
	log.Printf("[%s] translate done: %d segments, %d chunks, %d via fallback",
		job.VideoID, len(outcome.Segments), len(chunks), outcome.FallbackChunks)

	output := struct {
		VideoID        string          `json:"video_id"`
		SourceLanguage string          `json:"source_language"`
		TargetLanguage string          `json:"target_language"`
		Provider       string          `json:"provider"`
		TotalChunks    int             `json:"total_chunks"`
		FallbackChunks int             `json:"fallback_chunks"`
		Segments       []translatedSeg `json:"segments"`
	}{
		VideoID:        job.VideoID,
		SourceLanguage: transcript.Language,
		TargetLanguage: "vi-VN",
		Provider:       providerName,
		TotalChunks:    len(chunks),
		FallbackChunks: outcome.FallbackChunks,
		Segments:       make([]translatedSeg, len(outcome.Segments)),
	}
	for i, s := range outcome.Segments {
		output.Segments[i] = translatedSeg{Text: s.Text, OriginalText: s.OriginalText, Start: s.Start, Duration: s.Duration, Speaker: s.Speaker}
	}

	outBytes, _ := json.MarshalIndent(output, "", "  ")
	// Write then rename, so a crash never leaves a half-written file that a
	// later run would take as a valid cache.
	tmpPath := outputPath + ".tmp"
	if err := os.WriteFile(tmpPath, outBytes, 0644); err != nil {
		return fmt.Errorf("write translated: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("write translated: %w", err)
	}
	return nil
}

type translatedSeg struct {
	Text         string  `json:"text"`
	OriginalText string  `json:"original_text,omitempty"`
	Start        float64 `json:"start"`
	Duration     float64 `json:"duration"`
	Speaker      string  `json:"speaker,omitempty"`
}

type translateOutcome struct {
	Segments       []translator.TranslatedSegment // one per input segment, in order
	FallbackChunks int                            // chunks translated by the fallback provider
	FallbackReason error                          // first primary error that caused a fallback
}

// translateAll translates every chunk with primary, and with fallback (may be
// nil) for chunks primary cannot translate. It never returns untranslated
// text: when a chunk fails on every provider, or primary rejects the request
// permanently (bad key, no balance), everything stops and an error is returned.
func translateAll(ctx context.Context, videoID string, chunks []translator.Chunk, primary, fallback translator.TranslationProvider) (*translateOutcome, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		out       translateOutcome
		completed int
		results   = make([][]translator.TranslatedSegment, len(chunks))
		sem       = make(chan struct{}, translateConcurrency)
	)
	total := len(chunks)

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, chunk translator.Chunk) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return // another chunk already failed
			}

			segs, err := translateChunk(ctx, primary, chunk)
			usedFallback := false
			if err != nil && fallback != nil && !translator.IsPermanent(err) && ctx.Err() == nil {
				log.Printf("[%s] translate chunk %d/%d failed, trying fallback: %v", videoID, idx+1, total, err)
				primaryErr := err
				if segs, err = translateChunk(ctx, fallback, chunk); err == nil {
					usedFallback = true
					mu.Lock()
					if out.FallbackReason == nil {
						out.FallbackReason = primaryErr
					}
					mu.Unlock()
				} else {
					err = fmt.Errorf("%w (fallback also failed: %v)", primaryErr, err)
				}
			}
			if err != nil {
				cancel(fmt.Errorf("chunk %d/%d: %w", idx+1, total, err))
				return
			}

			mu.Lock()
			defer mu.Unlock()
			results[idx] = segs
			if usedFallback {
				out.FallbackChunks++
			}
			completed++
			log.Printf("[%s] translate chunk %d/%d done (%d/%d)", videoID, idx+1, total, completed, total)
		}(i, chunk)
	}
	wg.Wait()

	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	for _, segs := range results {
		out.Segments = append(out.Segments, segs...)
	}
	return &out, nil
}

// translateChunk translates one chunk and checks the provider kept one output
// segment per input segment, since timestamps are matched by position.
func translateChunk(ctx context.Context, provider translator.TranslationProvider, chunk translator.Chunk) ([]translator.TranslatedSegment, error) {
	segs, _, err := provider.Translate(ctx, []translator.Chunk{chunk}, nil, "vi-VN")
	if err == nil && len(segs) != len(chunk.Segments) {
		err = fmt.Errorf("provider returned %d segments for %d inputs", len(segs), len(chunk.Segments))
	}
	return segs, err
}
