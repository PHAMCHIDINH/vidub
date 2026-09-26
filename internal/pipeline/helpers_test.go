package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFresh(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldInput := write("transcript.json", 2*time.Hour)
	output := write("translated.json", time.Hour)
	newInput := write("new-transcript.json", 0)

	if !fresh(output, oldInput) {
		t.Error("output newer than its input should be fresh")
	}
	if fresh(output, oldInput, newInput) {
		t.Error("output older than an input should be stale")
	}
	if !fresh(output, filepath.Join(dir, "missing.json")) {
		t.Error("missing inputs are ignored")
	}
	if fresh(filepath.Join(dir, "missing-output"), oldInput) {
		t.Error("missing output is never fresh")
	}
}

func TestResultFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"dub.mp4", "subtitle.srt", "translated.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}

	var got []string
	for _, f := range ResultFiles(dir) {
		got = append(got, f.Name)
	}
	if len(got) != 2 || got[0] != "dub.mp4" || got[1] != "subtitle.srt" {
		t.Errorf("ResultFiles = %v, want [dub.mp4 subtitle.srt]", got)
	}
	if IsResultFile("translated.json") || IsResultFile("../x") || !IsResultFile("voiceover.mp4") {
		t.Error("IsResultFile allows the wrong names")
	}
}
