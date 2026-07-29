package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"vidub/internal/translator"
)

func (p *Pipeline) stepTranslate(ctx context.Context, job *VideoJob) error {
	inputPath := filepath.Join(job.WorkDir, "transcript.json")
	outputPath := filepath.Join(job.WorkDir, "translated.json")
	if fileExists(outputPath) {
		log.Printf("[%s] translate skipped (translated.json exists)", job.VideoID)
		return nil
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
		segs[i] = translator.Segment{Text: s.Text, Start: s.Start, Duration: s.Duration}
	}

	apiKey := job.APIKey
	if apiKey == "" {
		apiKey = p.cfg.DeepSeekAPIKey
	}

	var provider translator.TranslationProvider
	var providerName string
	if apiKey != "" {
		provider = translator.NewDeepSeekProvider(apiKey)
		providerName = "deepseek"
	} else {
		provider = translator.NewGoogleProvider("vi")
		providerName = "google"
	}
	chunks := translator.ChunkTranscript(segs, translator.DefaultTargetWords, translator.DefaultMaxWords, translator.DefaultMaxDurationSecs)
	for i := range chunks {
		chunks[i].Context = translator.BuildContextWindow(chunks, i, translator.DefaultContextLines)
	}

	totalChunks := len(chunks)
	log.Printf("[%s] translating %d chunks in parallel (provider=%s)...", job.VideoID, totalChunks, providerName)
	type chunkResult struct {
		index    int
		segments []translator.TranslatedSegment
	}

	results := make([]*chunkResult, totalChunks)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var completed int
	sem := make(chan struct{}, 5)

	for i := 0; i < totalChunks; i++ {
		wg.Add(1)
		go func(idx int, chunk translator.Chunk) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			segs, _, err := provider.Translate(ctx, []translator.Chunk{chunk}, nil, "vi-VN")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				log.Printf("[%s] translate chunk %d/%d failed: %v", job.VideoID, idx+1, totalChunks, err)
				fallback := make([]translator.TranslatedSegment, len(chunk.Segments))
				for j, s := range chunk.Segments {
					fallback[j] = translator.TranslatedSegment{Text: s.Text, OriginalText: s.Text, Start: s.Start, Duration: s.Duration}
				}
				results[idx] = &chunkResult{index: idx, segments: fallback}
			} else {
				for j := range segs {
					if j < len(chunk.Segments) {
						if segs[j].Start == 0 && segs[j].Duration == 0 {
							segs[j].Start = chunk.Segments[j].Start
							segs[j].Duration = chunk.Segments[j].Duration
						}
						if segs[j].Speaker == "" {
							segs[j].Speaker = chunk.Segments[j].Speaker
						}
						if segs[j].OriginalText == "" {
							segs[j].OriginalText = chunk.Segments[j].Text
						}
					}
				}
				results[idx] = &chunkResult{index: idx, segments: segs}
			}
			completed++
			log.Printf("[%s] translate chunk %d/%d done (%d/%d)", job.VideoID, idx+1, totalChunks, completed, totalChunks)
		}(i, chunks[i])
	}
	wg.Wait()

	var allSegments []translator.TranslatedSegment
	for i := 0; i < totalChunks; i++ {
		if results[i] != nil {
			allSegments = append(allSegments, results[i].segments...)
		}
	}

	log.Printf("[%s] translate done: %d segments translated", job.VideoID, len(allSegments))

	output := struct {
		VideoID        string          `json:"video_id"`
		SourceLanguage string          `json:"source_language"`
		TargetLanguage string          `json:"target_language"`
		Provider       string          `json:"provider"`
		Segments       []translatedSeg `json:"segments"`
	}{
		VideoID:        job.VideoID,
		SourceLanguage: transcript.Language,
		TargetLanguage: "vi-VN",
		Provider:       providerName,
		Segments:       make([]translatedSeg, len(allSegments)),
	}
	for i, s := range allSegments {
		output.Segments[i] = translatedSeg{Text: s.Text, OriginalText: s.OriginalText, Start: s.Start, Duration: s.Duration, Speaker: s.Speaker}
	}

	outBytes, _ := json.MarshalIndent(output, "", "  ")
	if err := os.WriteFile(outputPath, outBytes, 0644); err != nil {
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
