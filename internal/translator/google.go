package translator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GoogleProvider implements TranslationProvider using the free Google Translate HTTP API.
type GoogleProvider struct {
	client     *http.Client
	targetLang string
}

// NewGoogleProvider creates a Google Translate provider.
func NewGoogleProvider(targetLang string) *GoogleProvider {
	return &GoogleProvider{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		targetLang: targetLang,
	}
}

// Name returns the provider name.
func (p *GoogleProvider) Name() string { return "google" }

// Model returns an empty string (Google Translate is not model-based).
func (p *GoogleProvider) Model() string { return "google-translate" }

// Translate implements TranslationProvider.Translate.
func (p *GoogleProvider) Translate(ctx context.Context, chunks []Chunk, glossary []GlossaryTerm, targetLang string) ([]TranslatedSegment, TokenUsage, error) {
	if len(chunks) == 0 {
		return nil, TokenUsage{}, nil
	}

	var allSegments []TranslatedSegment

	for _, chunk := range chunks {
		if len(chunk.Segments) == 0 {
			continue
		}

		segs, err := p.translateChunk(ctx, chunk)
		if err != nil {
			return nil, TokenUsage{}, fmt.Errorf("google translate: %w", err)
		}
		allSegments = append(allSegments, segs...)

		// Rate limit: be polite to Google's free API.
		select {
		case <-ctx.Done():
			return allSegments, TokenUsage{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return allSegments, TokenUsage{Model: "google-translate"}, nil
}

// googleSeparator joins a chunk into one request. Google usually keeps it,
// but sometimes drops or rewrites one, so the count is always checked.
const googleSeparator = "|||"

// translateChunk returns one TranslatedSegment per input segment.
func (p *GoogleProvider) translateChunk(ctx context.Context, chunk Chunk) ([]TranslatedSegment, error) {
	parts := make([]string, len(chunk.Segments))
	for i, seg := range chunk.Segments {
		parts[i] = seg.Text
	}

	joined, err := p.translateText(ctx, strings.Join(parts, " "+googleSeparator+" "))
	if err != nil {
		return nil, err
	}

	texts := strings.Split(joined, googleSeparator)
	if len(texts) != len(parts) {
		// A separator got lost. Mapping by position would shift every later
		// line onto the wrong timestamp, so translate each segment on its own.
		texts = make([]string, len(parts))
		for i, part := range parts {
			if strings.TrimSpace(part) == "" {
				continue
			}
			if texts[i], err = p.translateText(ctx, part); err != nil {
				return nil, err
			}
		}
	}

	out := make([]TranslatedSegment, len(chunk.Segments))
	for i, seg := range chunk.Segments {
		out[i] = TranslatedSegment{
			Text:         strings.TrimSpace(texts[i]),
			OriginalText: seg.Text,
			Start:        seg.Start,
			Duration:     seg.Duration,
			Speaker:      seg.Speaker,
		}
	}
	return out, nil
}

// googleAttempts is the number of tries per request. The free endpoint
// rate-limits and fails transiently under parallel load.
const googleAttempts = 3

// translateText translates one piece of text, retrying transient failures.
func (p *GoogleProvider) translateText(ctx context.Context, text string) (string, error) {
	var err error
	for attempt := 0; attempt < googleAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		var out string
		if out, err = p.translateOnce(ctx, text); err == nil {
			return out, nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && !apiErr.Retryable() {
			break // e.g. 400: the same request will fail again
		}
	}
	return "", err
}

func (p *GoogleProvider) translateOnce(ctx context.Context, text string) (string, error) {
	params := url.Values{}
	params.Set("client", "gtx")
	params.Set("sl", "auto")
	params.Set("tl", p.targetLang)
	params.Set("dt", "t")
	params.Set("q", text)

	apiURL := "https://translate.googleapis.com/translate_a/single?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", &APIError{Provider: "google", Status: resp.StatusCode, Body: truncateStr(string(body), 200)}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	// Google Translate response format: [[["translated", "original", ...], null, ...], null, "en"]
	var result []interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w (body: %s)", err, truncateStr(string(body), 200))
	}

	if len(result) == 0 {
		return "", fmt.Errorf("empty response from Google Translate")
	}

	first, ok := result[0].([]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected response format")
	}

	var translated strings.Builder
	for i, entry := range first {
		entryArr, ok := entry.([]interface{})
		if !ok || len(entryArr) == 0 {
			continue
		}
		translatedText, _ := entryArr[0].(string)
		if i > 0 {
			translated.WriteString(" ")
		}
		translated.WriteString(translatedText)
	}

	return translated.String(), nil
}

// Ensure interface compliance.
var _ TranslationProvider = (*GoogleProvider)(nil)

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
