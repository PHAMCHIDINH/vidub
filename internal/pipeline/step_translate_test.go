package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"vidub/internal/translator"
)

// fakeProvider translates "x" to "vi:x", or fails for chunks whose first
// segment text is in failOn.
type fakeProvider struct {
	failOn map[string]error
	calls  atomic.Int32
}

func (f *fakeProvider) Translate(_ context.Context, chunks []translator.Chunk, _ []translator.GlossaryTerm, _ string) ([]translator.TranslatedSegment, translator.TokenUsage, error) {
	f.calls.Add(1)
	var out []translator.TranslatedSegment
	for _, c := range chunks {
		if err := f.failOn[c.Segments[0].Text]; err != nil {
			return nil, translator.TokenUsage{}, err
		}
		for _, s := range c.Segments {
			out = append(out, translator.TranslatedSegment{Text: "vi:" + s.Text, OriginalText: s.Text, Start: s.Start})
		}
	}
	return out, translator.TokenUsage{}, nil
}

func testChunks(texts ...string) []translator.Chunk {
	chunks := make([]translator.Chunk, len(texts))
	for i, t := range texts {
		chunks[i] = translator.Chunk{Segments: []translator.Segment{{Text: t, Start: float64(i)}}}
	}
	return chunks
}

func TestTranslateAllUsesFallbackForFailedChunks(t *testing.T) {
	primary := &fakeProvider{failOn: map[string]error{"b": errors.New("rate limited")}}
	fallback := &fakeProvider{}

	out, err := translateAll(context.Background(), "test", testChunks("a", "b", "c"), primary, fallback)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, s := range out.Segments {
		got = append(got, s.Text)
	}
	if strings.Join(got, ",") != "vi:a,vi:b,vi:c" {
		t.Errorf("segments = %v, want every chunk translated in order", got)
	}
	if out.FallbackChunks != 1 || out.FallbackReason == nil {
		t.Errorf("FallbackChunks = %d, reason = %v; want 1 and the primary error", out.FallbackChunks, out.FallbackReason)
	}
}

func TestTranslateAllFailsInsteadOfKeepingOriginalText(t *testing.T) {
	down := errors.New("down")
	primary := &fakeProvider{failOn: map[string]error{"b": down}}
	fallback := &fakeProvider{failOn: map[string]error{"b": down}}

	out, err := translateAll(context.Background(), "test", testChunks("a", "b", "c"), primary, fallback)
	if err == nil {
		t.Fatalf("expected an error, got %d segments", len(out.Segments))
	}
	if !strings.Contains(err.Error(), "chunk 2/3") {
		t.Errorf("error should name the failed chunk: %v", err)
	}
}

func TestTranslateAllDoesNotFallBackOnRejectedKey(t *testing.T) {
	badKey := &translator.APIError{Provider: "deepseek", Status: 401, Body: "invalid key"}
	primary := &fakeProvider{failOn: map[string]error{"a": badKey}}
	fallback := &fakeProvider{}

	_, err := translateAll(context.Background(), "test", testChunks("a"), primary, fallback)
	if !translator.IsPermanent(err) {
		t.Fatalf("err = %v, want the permanent API error", err)
	}
	if n := fallback.calls.Load(); n != 0 {
		t.Errorf("fallback called %d times; a rejected key must not silently switch provider", n)
	}
}
