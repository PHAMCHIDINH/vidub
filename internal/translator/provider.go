package translator

import "context"

// Segment represents a single timestamped piece of transcript text.
type Segment struct {
	Text     string  `json:"text"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Speaker  string  `json:"speaker,omitempty"`
	// MaxChars is the longest translation that can be spoken in time
	// (see SetCharBudgets). Zero means no limit.
	MaxChars int `json:"max_chars,omitempty"`
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
