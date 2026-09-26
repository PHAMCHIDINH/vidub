package storage

import (
	"encoding/json"
	"os"
	"path/filepath"

	"vidub/internal/store"
)

// metaFile holds a job's StoredJob inside its directory, so the history
// survives a server restart. The store itself stays in memory (ADR-0003).
const metaFile = "job.json"

// SaveJobMeta writes job to <dir>/job.json. It writes then renames, so a crash
// never leaves a half-written file.
func SaveJobMeta(dir string, job store.StoredJob) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, metaFile+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, metaFile))
}

// LoadJobs rebuilds the job list from the job directories in storageDir.
// Jobs saved as running were interrupted by a restart and come back as
// failed. Directories from before job.json existed are inferred from their
// output files.
func LoadJobs(storageDir string) []store.StoredJob {
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		return nil // no storage dir yet
	}

	var jobs []store.StoredJob
	for _, e := range entries {
		if !e.IsDir() || !store.ValidJobID(e.Name()) {
			continue
		}
		dir := filepath.Join(storageDir, e.Name())

		job, ok := readJobMeta(dir)
		if !ok || job.VideoID != e.Name() {
			info, err := e.Info()
			if err != nil {
				continue
			}
			job = store.StoredJob{
				VideoID:   e.Name(),
				URL:       "https://www.youtube.com/watch?v=" + e.Name(),
				Status:    inferStatus(dir),
				CreatedAt: info.ModTime(),
			}
		}
		if job.Status == store.JobRunning {
			job.Status = store.JobFailed
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func readJobMeta(dir string) (store.StoredJob, bool) {
	var job store.StoredJob
	data, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil || json.Unmarshal(data, &job) != nil {
		return store.StoredJob{}, false
	}
	return job, true
}

// inferStatus guesses the status of a directory without job.json: the dub
// step writes the final videos, so either one means the pipeline finished.
func inferStatus(dir string) store.JobStatus {
	for _, name := range []string{"voiceover.mp4", "dub.mp4"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return store.JobCompleted
		}
	}
	return store.JobFailed
}
