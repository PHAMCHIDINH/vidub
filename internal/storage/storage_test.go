package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"vidub/internal/store"
)

func mkJobDir(t *testing.T, root, name string, files ...string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadJobs(t *testing.T) {
	root := t.TempDir()

	interrupted := mkJobDir(t, root, "aaaaaaaaaaa")
	if err := SaveJobMeta(interrupted, store.StoredJob{VideoID: "aaaaaaaaaaa", Mode: "replace", Status: store.JobRunning}); err != nil {
		t.Fatal(err)
	}
	mkJobDir(t, root, "bbbbbbbbbbb", "dub.mp4")         // older run without job.json, finished
	mkJobDir(t, root, "ccccccccccc", "transcript.json") // older run without job.json, unfinished
	mkJobDir(t, root, "not-a-job")                      // ignored

	got := map[string]store.StoredJob{}
	for _, j := range LoadJobs(root) {
		got[j.VideoID] = j
	}

	if len(got) != 3 {
		t.Fatalf("loaded %d jobs, want 3: %v", len(got), got)
	}
	if j := got["aaaaaaaaaaa"]; j.Status != store.JobFailed || j.Mode != "replace" {
		t.Errorf("interrupted job = %+v, want failed with its saved mode", j)
	}
	if j := got["bbbbbbbbbbb"]; j.Status != store.JobCompleted {
		t.Errorf("finished job status = %q, want completed", j.Status)
	}
	if j := got["ccccccccccc"]; j.Status != store.JobFailed {
		t.Errorf("unfinished job status = %q, want failed", j.Status)
	}
}

func TestCleanupOldJobs(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	mkOld := func(name string) {
		dir := mkJobDir(t, root, name)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}

	mkOld("aaaaaaaaaaa")             // old and idle: removed
	mkOld("bbbbbbbbbbb")             // old but running: kept
	mkOld("keep-me")                 // not a job directory: kept
	mkJobDir(t, root, "ccccccccccc") // recent: kept

	jobs := store.NewJobStore()
	jobs.Restore(store.StoredJob{VideoID: "aaaaaaaaaaa", Status: store.JobCompleted})
	jobs.Start("bbbbbbbbbbb", "", "", "")

	done := make(chan struct{})
	defer close(done)
	CleanupOldJobs(root, 24*time.Hour, jobs, done) // first pass runs synchronously

	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	if exists("aaaaaaaaaaa") {
		t.Error("old idle job was not removed")
	}
	for _, name := range []string{"bbbbbbbbbbb", "keep-me", "ccccccccccc"} {
		if !exists(name) {
			t.Errorf("%s was removed", name)
		}
	}
	if _, ok := jobs.Get("aaaaaaaaaaa"); ok {
		t.Error("removed job is still in the store")
	}
}
