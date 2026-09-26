package translator

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTranslations(t *testing.T) {
	tests := []struct {
		name     string
		response string
		n        int
		want     map[int]string
		wantErr  bool
	}{
		{"ids in order", `{"segments":[{"id":1,"text":"a"},{"id":2,"text":"b"}]}`, 2, map[int]string{0: "a", 1: "b"}, false},
		{"ids out of order", `{"segments":[{"id":2,"text":"b"},{"id":1,"text":"a"}]}`, 2, map[int]string{0: "a", 1: "b"}, false},
		{"merged line leaves the other id missing", `{"segments":[{"id":1,"text":"a b"}]}`, 2, map[int]string{0: "a b"}, false},
		{"string ids", `{"segments":[{"id":"1","text":"a"},{"id":"2","text":"b"}]}`, 2, map[int]string{0: "a", 1: "b"}, false},
		{"unknown and duplicate ids ignored", `{"segments":[{"id":1,"text":"a"},{"id":1,"text":"x"},{"id":9,"text":"z"}]}`, 2, map[int]string{0: "a"}, false},
		{"empty text counts as missing", `{"segments":[{"id":1,"text":"  "},{"id":2,"text":"b"}]}`, 2, map[int]string{1: "b"}, false},
		{"markdown fence", "```json\n{\"segments\":[{\"id\":1,\"text\":\"a\"}]}\n```", 1, map[int]string{0: "a"}, false},
		{"bare array with ids", `[{"id":1,"text":"a"},{"id":2,"text":"b"}]`, 2, map[int]string{0: "a", 1: "b"}, false},
		{"pairs without ids, same count", `[["a","x"],["b","y"]]`, 2, map[int]string{0: "a", 1: "b"}, false},
		{"no ids and wrong count is an error", `{"segments":[{"text":"a"}]}`, 2, nil, true},
		{"not json", `sorry, I cannot help`, 1, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTranslations(tt.response, tt.n)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildUserPromptKeepsLineBreaksInsideOneSegment(t *testing.T) {
	chunk := Chunk{Segments: []Segment{{Text: "line one\nline two", MaxChars: 40}, {Text: "a <b> & c"}}}
	prompt := BuildUserPrompt(chunk, nil, DefaultContextLines)

	for _, want := range []string{
		`"id": 1`,
		`"text": "line one\nline two"`, // escaped inside the JSON string
		`"text": "a <b> & c"`,          // not HTML-escaped
		"đúng 2 phần tử",
		`"max_chars": 40`, // the length budget reaches the model
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
