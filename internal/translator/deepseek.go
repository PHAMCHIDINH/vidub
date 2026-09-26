package translator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DeepSeek API constants.
const (
	deepseekBaseURL = "https://api.deepseek.com/v1"
	deepseekModel   = "deepseek-v4-flash"
)

// DeepSeekProvider implements TranslationProvider using the DeepSeek API.
// It is the default provider.
type DeepSeekProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewDeepSeekProvider creates a new DeepSeekProvider.
// An empty model means the default model.
func NewDeepSeekProvider(apiKey, model string) *DeepSeekProvider {
	if model == "" {
		model = deepseekModel
	}
	return &DeepSeekProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: deepseekBaseURL,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Name returns the provider name for metadata.
func (p *DeepSeekProvider) Name() string {
	return "deepseek"
}

// Model returns the model name for metadata.
func (p *DeepSeekProvider) Model() string {
	return p.model
}

// deepSeekMessage represents a chat message in the DeepSeek API request.
type deepSeekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// deepSeekRequest represents the DeepSeek API request body.
type deepSeekRequest struct {
	Model       string            `json:"model"`
	Messages    []deepSeekMessage `json:"messages"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
}

// deepSeekChoice represents a choice in the DeepSeek API response.
type deepSeekChoice struct {
	Message deepSeekMessage `json:"message"`
}

// deepSeekUsage represents token usage from the DeepSeek API.
type deepSeekUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// deepSeekResponse represents the DeepSeek API response.
type deepSeekResponse struct {
	Choices []deepSeekChoice `json:"choices"`
	Usage   deepSeekUsage    `json:"usage"`
}

// maxChunkAttempts is the number of API calls allowed per chunk. Calls after
// the first only ask for the segments that are still missing.
const maxChunkAttempts = 3

// Translate implements TranslationProvider.Translate.
// It returns exactly one TranslatedSegment per input segment, in input order.
func (p *DeepSeekProvider) Translate(ctx context.Context, chunks []Chunk, glossary []GlossaryTerm, targetLang string) ([]TranslatedSegment, TokenUsage, error) {
	if len(chunks) == 0 {
		return nil, TokenUsage{}, nil
	}

	systemPrompt := BuildSystemPrompt(targetLang)
	totalUsage := TokenUsage{Model: p.model}
	var allSegments []TranslatedSegment

	for _, chunk := range chunks {
		segs, usage, err := p.translateChunk(ctx, systemPrompt, chunk, glossary)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		if err != nil {
			return nil, totalUsage, err
		}
		allSegments = append(allSegments, segs...)
	}

	// Calculate cost using DeepSeek pricing: $0.14/1M input, $0.28/1M output (estimate).
	totalUsage.CostUSD = (float64(totalUsage.InputTokens)/1_000_000)*0.14 +
		(float64(totalUsage.OutputTokens)/1_000_000)*0.28

	return allSegments, totalUsage, nil
}

// translateChunk returns one TranslatedSegment per input segment, carrying the
// input timestamps. Translations are matched by id, so a merged or skipped
// line can never shift text onto another segment's timestamp. Segments the
// model skips are requested again, on their own, until maxChunkAttempts.
func (p *DeepSeekProvider) translateChunk(ctx context.Context, systemPrompt string, chunk Chunk, glossary []GlossaryTerm) ([]TranslatedSegment, TokenUsage, error) {
	var usage TokenUsage
	texts := make([]string, len(chunk.Segments))

	var pending []int // indexes into chunk.Segments still without a translation
	for i, seg := range chunk.Segments {
		if strings.TrimSpace(seg.Text) != "" {
			pending = append(pending, i)
		}
	}

	var lastErr error
	for attempt := 0; attempt < maxChunkAttempts && len(pending) > 0; attempt++ {
		if attempt > 0 {
			select { // back off a little, mostly for rate limits
			case <-ctx.Done():
				return nil, usage, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		sub := Chunk{Context: chunk.Context, Segments: make([]Segment, len(pending))}
		for k, idx := range pending {
			sub.Segments[k] = chunk.Segments[idx]
		}

		content, u, err := p.complete(ctx, systemPrompt, BuildUserPrompt(sub, glossary, DefaultContextLines))
		usage.InputTokens += u.InputTokens
		usage.OutputTokens += u.OutputTokens
		if err != nil {
			if IsPermanent(err) {
				return nil, usage, err // a bad key stays bad; do not retry
			}
			lastErr = err
			continue
		}

		got, err := ParseTranslations(content, len(sub.Segments))
		if err != nil {
			lastErr = err
			continue
		}

		var missing []int
		for k, idx := range pending {
			if text, ok := got[k]; ok {
				texts[idx] = text
			} else {
				missing = append(missing, idx)
			}
		}
		if len(missing) > 0 {
			lastErr = fmt.Errorf("model skipped %d of %d segments", len(missing), len(pending))
		}
		pending = missing
	}

	if len(pending) > 0 {
		return nil, usage, fmt.Errorf("%d of %d segments untranslated after %d attempts: %w",
			len(pending), len(chunk.Segments), maxChunkAttempts, lastErr)
	}

	out := make([]TranslatedSegment, len(chunk.Segments))
	for i, seg := range chunk.Segments {
		out[i] = TranslatedSegment{
			Text:         texts[i],
			OriginalText: seg.Text,
			Start:        seg.Start,
			Duration:     seg.Duration,
			Speaker:      seg.Speaker,
		}
	}
	return out, usage, nil
}

// complete sends one chat completion request and returns the message content.
func (p *DeepSeekProvider) complete(ctx context.Context, systemPrompt, userPrompt string) (string, TokenUsage, error) {
	reqBody := deepSeekRequest{
		Model: p.model,
		Messages: []deepSeekMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   8192,
		Temperature: 0.3,
	}

	payloadBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(payloadBytes))
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", TokenUsage{}, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", TokenUsage{}, &APIError{Provider: "deepseek", Status: resp.StatusCode, Body: truncateStr(string(bodyBytes), 300)}
	}

	var apiResp deepSeekResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return "", TokenUsage{}, fmt.Errorf("parse API response: %w", err)
	}
	usage := TokenUsage{InputTokens: apiResp.Usage.PromptTokens, OutputTokens: apiResp.Usage.CompletionTokens}
	if len(apiResp.Choices) == 0 {
		return "", usage, fmt.Errorf("API returned no choices")
	}
	return apiResp.Choices[0].Message.Content, usage, nil
}

// ensure interfaces match at compile time
var _ TranslationProvider = (*DeepSeekProvider)(nil)
