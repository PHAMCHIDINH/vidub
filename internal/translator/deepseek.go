package translator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// DeepSeek API constants.
const (
	deepseekBaseURL = "https://api.deepseek.com/v1"
	deepseekModel   = "deepseek-v4-flash"
	envDeepSeekKey  = "DEEPSEEK_API_KEY"
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
// The API key is read from the DEEPSEEK_API_KEY environment variable if not provided.
func NewDeepSeekProvider(apiKey string) *DeepSeekProvider {
	if apiKey == "" {
		apiKey = os.Getenv(envDeepSeekKey)
	}
	model := os.Getenv("DEEPSEEK_MODEL")
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

// Translate implements TranslationProvider.Translate.
// It sends each chunk to the DeepSeek API and returns the translated segments.
func (p *DeepSeekProvider) Translate(ctx context.Context, chunks []Chunk, glossary []GlossaryTerm, targetLang string) ([]TranslatedSegment, TokenUsage, error) {
	if len(chunks) == 0 {
		return nil, TokenUsage{}, nil
	}

	systemPrompt := BuildSystemPrompt(targetLang)
	contextLines := DefaultContextLines

	var allSegments []TranslatedSegment
	totalUsage := TokenUsage{
		Model: p.model,
	}

	for _, chunk := range chunks {
		if len(chunk.Segments) == 0 {
			continue
		}

		userPrompt := BuildUserPrompt(chunk, glossary, contextLines)

		// Build API request payload.
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
			return nil, totalUsage, fmt.Errorf("marshal request: %w", err)
		}

		// Create HTTP request.
		httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(payloadBytes))
		if err != nil {
			return nil, totalUsage, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

		// Execute request.
		resp, err := p.client.Do(httpReq)
		if err != nil {
			return nil, totalUsage, fmt.Errorf("API request failed: %w", err)
		}
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, totalUsage, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, totalUsage, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(bodyBytes))
		}

		// Parse API response.
		var apiResp deepSeekResponse
		if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
			return nil, totalUsage, fmt.Errorf("parse API response: %w", err)
		}

		if len(apiResp.Choices) == 0 {
			return nil, totalUsage, fmt.Errorf("API returned no choices")
		}

		content := apiResp.Choices[0].Message.Content

		// Parse the translated segments from the response content.
		parsed, err := ParseTranslationResponse(content, len(chunk.Segments))
		if err != nil {
			return nil, totalUsage, fmt.Errorf("parse chunk response: %w", err)
		}

		// Map timestamps and speakers from input segments.
		for i := range parsed {
			if i < len(chunk.Segments) {
				parsed[i].Start = chunk.Segments[i].Start
				parsed[i].Duration = chunk.Segments[i].Duration
				parsed[i].Speaker = chunk.Segments[i].Speaker
				if parsed[i].OriginalText == "" {
					parsed[i].OriginalText = chunk.Segments[i].Text
				}
			}
		}

		allSegments = append(allSegments, parsed...)

		totalUsage.InputTokens += apiResp.Usage.PromptTokens
		totalUsage.OutputTokens += apiResp.Usage.CompletionTokens
	}

	// Calculate cost using DeepSeek pricing: $0.14/1M input, $0.28/1M output (estimate).
	totalUsage.CostUSD = (float64(totalUsage.InputTokens)/1_000_000)*0.14 +
		(float64(totalUsage.OutputTokens)/1_000_000)*0.28

	return allSegments, totalUsage, nil
}

// ensure interfaces match at compile time
var _ TranslationProvider = (*DeepSeekProvider)(nil)
