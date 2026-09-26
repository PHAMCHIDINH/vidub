package translator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeDeepSeek answers chat completions with the given contents in order
// (repeating the last one) and records the user prompts it received.
func fakeDeepSeek(t *testing.T, contents ...string) (*DeepSeekProvider, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req deepSeekRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		prompts = append(prompts, req.Messages[1].Content)
		content := contents[min(len(prompts), len(contents))-1]
		mu.Unlock()
		json.NewEncoder(w).Encode(deepSeekResponse{
			Choices: []deepSeekChoice{{Message: deepSeekMessage{Role: "assistant", Content: content}}},
		})
	}))
	t.Cleanup(srv.Close)

	p := NewDeepSeekProvider("test-key", "")
	p.baseURL = srv.URL
	return p, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), prompts...)
	}
}

func threeSegments() Chunk {
	return Chunk{Segments: []Segment{
		{Text: "one", Start: 1, Duration: 1},
		{Text: "two", Start: 2, Duration: 1},
		{Text: "three", Start: 3, Duration: 1},
	}}
}

func TestDeepSeekRetriesOnlySkippedSegments(t *testing.T) {
	p, prompts := fakeDeepSeek(t,
		`{"segments":[{"id":1,"text":"một"},{"id":3,"text":"ba"}]}`, // id 2 skipped
		`{"segments":[{"id":1,"text":"hai"}]}`,                      // retry only has "two", renumbered as 1
	)

	got, _, err := p.Translate(context.Background(), []Chunk{threeSegments()}, nil, "vi-VN")
	if err != nil {
		t.Fatal(err)
	}

	wantText := []string{"một", "hai", "ba"}
	if len(got) != len(wantText) {
		t.Fatalf("got %d segments, want %d", len(got), len(wantText))
	}
	for i, seg := range got {
		if seg.Text != wantText[i] || seg.Start != float64(i+1) {
			t.Errorf("segment %d = %q at %.0fs, want %q at %ds", i, seg.Text, seg.Start, wantText[i], i+1)
		}
	}

	sent := prompts()
	if len(sent) != 2 {
		t.Fatalf("made %d API calls, want 2", len(sent))
	}
	if !strings.Contains(sent[1], `"two"`) || strings.Contains(sent[1], `"one"`) || strings.Contains(sent[1], `"three"`) {
		t.Errorf("retry should only ask for the skipped segment, got:\n%s", sent[1])
	}
}

func TestDeepSeekGivesUpAfterMaxAttempts(t *testing.T) {
	// Every answer uses an id that does not exist, so nothing ever maps.
	p, prompts := fakeDeepSeek(t, `{"segments":[{"id":99,"text":"x"}]}`)

	if _, _, err := p.Translate(context.Background(), []Chunk{threeSegments()}, nil, "vi-VN"); err == nil {
		t.Fatal("expected an error when segments stay untranslated")
	}
	if n := len(prompts()); n != maxChunkAttempts {
		t.Errorf("made %d API calls, want %d", n, maxChunkAttempts)
	}
}

func TestDeepSeekDoesNotRetryRejectedKey(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":{"message":"Authentication Fails"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := NewDeepSeekProvider("bad-key", "")
	p.baseURL = srv.URL

	_, _, err := p.Translate(context.Background(), []Chunk{threeSegments()}, nil, "vi-VN")
	if !IsPermanent(err) {
		t.Fatalf("err = %v, want a permanent APIError", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("made %d API calls, want 1", n)
	}
}
