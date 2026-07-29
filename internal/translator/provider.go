package translator

import "context"

// Segment represents a single timestamped piece of transcript text.
type Segment struct {
	Text     string  `json:"text"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Speaker  string  `json:"speaker,omitempty"`
}

// TranslatedSegment represents a translated transcript segment.
type TranslatedSegment struct {
	Text         string  `json:"text"`
	OriginalText string  `json:"original_text"`
	Start        float64 `json:"start"`
	Duration     float64 `json:"duration"`
	Speaker      string  `json:"speaker,omitempty"`
}

// GlossaryTerm represents a RAG glossary entry for translation.
type GlossaryTerm struct {
	SourceTerm string
	TargetTerm string
}

// Chunk represents a group of consecutive segments for a single LLM API call.
type Chunk struct {
	Segments []Segment
	Context  string // Previous lines for translation coherence
}

// TokenUsage tracks token counts and cost for a translation request.
type TokenUsage struct {
	InputTokens  int     `json:"input"`
	OutputTokens int     `json:"output"`
	CostUSD      float64 `json:"cost_usd"`
	Model        string  `json:"model"`
}

// Transcript represents the full extracted transcript JSON file.
type Transcript struct {
	VideoID     string    `json:"video_id"`
	Language    string    `json:"language"`
	Source      string    `json:"source,omitempty"`
	Segments    []Segment `json:"segments"`
}

// TranslationResult is the top-level output structure written to disk.
type TranslationResult struct {
	VideoID        string              `json:"video_id"`
	SourceLanguage string              `json:"source_language"`
	TargetLanguage string              `json:"target_language"`
	Provider       string              `json:"provider"`
	Model          string              `json:"model"`
	TokenUsage     TokenUsage          `json:"token_usage"`
	ContextWindow  int                 `json:"context_window"`
	RAGTermsUsed   int                 `json:"rag_terms_used"`
	FailedChunks   int                 `json:"failed_chunks"`
	TotalChunks    int                 `json:"total_chunks"`
	Segments       []TranslatedSegment `json:"segments"`
	TranslatedAt   string              `json:"translated_at"`
}

// TranslationProvider is the interface for translation backends.
// All providers (DeepSeek, OpenAI, Google Translate, etc.) must implement this.
type TranslationProvider interface {
	// Translate translates the given chunks of transcript segments.
	// chunks: the transcript divided into chunks at sentence boundaries.
	// glossary: domain-specific terminology pairs to inject into the prompt.
	// targetLang: the target language code (e.g. "vi-VN", "vi").
	// Returns the flattened list of all translated segments in order.
	Translate(ctx context.Context, chunks []Chunk, glossary []GlossaryTerm, targetLang string) ([]TranslatedSegment, TokenUsage, error)
}
