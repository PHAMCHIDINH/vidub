package storage

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"vidub/internal/store"
)

const (
	defaultMaxAge = 24 * time.Hour
	cleanupTick   = 1 * time.Hour
)

// CleanupOldJobs removes job directories older than maxAge from storageDir.
// Runs once immediately, then every cleanupTick in the background.
// Stops when the done channel is closed.
//
// Only directories named like a job (a video ID) are touched, so a
// mistyped STORAGE_DIR cannot wipe unrelated folders. Running jobs are
// skipped, and removed jobs also leave the store (and so the sidebar).
// Age is the directory's mtime, which the pipeline refreshes on every run.
func CleanupOldJobs(storageDir string, maxAge time.Duration, jobs *store.JobStore, done <-chan struct{}) {
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}

	run := func() {
		entries, err := os.ReadDir(storageDir)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("[cleanup] read dir: %v", err)
			}
			return
		}

		cutoff := time.Now().Add(-maxAge)
		cleaned := 0

		for _, entry := range entries {
			if !entry.IsDir() || !store.ValidJobID(entry.Name()) {
				continue
			}
			info, err := entry.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				continue
			}

			dirPath := filepath.Join(storageDir, entry.Name())
			err = jobs.Remove(entry.Name(), func() error { return os.RemoveAll(dirPath) })
			switch {
			case errors.Is(err, store.ErrJobRunning):
				// Still in use; try again next tick.
			case err != nil:
				log.Printf("[cleanup] failed to remove %s: %v", entry.Name(), err)
			default:
				cleaned++
			}
		}

		if cleaned > 0 {
			log.Printf("[cleanup] removed %d old job(s) older than %v", cleaned, maxAge)
		}
	}

	// Run once at start.
	run()

	go func() {
		ticker := time.NewTicker(cleanupTick)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
