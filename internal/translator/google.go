package translator

import (
	"context"
	"encoding/json"
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

		// Build joined text with separator.
		var parts []string
		for _, seg := range chunk.Segments {
			parts = append(parts, seg.Text)
		}
		joined := strings.Join(parts, " ||| ")

		// Call Google Translate API.
		translated, err := p.translateText(ctx, joined)
		if err != nil {
			return nil, TokenUsage{}, fmt.Errorf("google translate: %w", err)
		}

		// Split result back into segments.
		transParts := strings.Split(translated, "|||")
		for i := range transParts {
			transParts[i] = strings.TrimSpace(transParts[i])
		}

		for i := range chunk.Segments {
			seg := TranslatedSegment{
				OriginalText: chunk.Segments[i].Text,
				Start:        chunk.Segments[i].Start,
				Duration:     chunk.Segments[i].Duration,
				Speaker:      chunk.Segments[i].Speaker,
			}
			if i < len(transParts) {
				seg.Text = transParts[i]
			} else {
				seg.Text = chunk.Segments[i].Text // fallback to original
			}
			allSegments = append(allSegments, seg)
		}

		// Rate limit: be polite to Google's free API.
		select {
		case <-ctx.Done():
			return allSegments, TokenUsage{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return allSegments, TokenUsage{Model: "google-translate"}, nil
}

func (p *GoogleProvider) translateText(ctx context.Context, text string) (string, error) {
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
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateStr(string(body), 200))
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
