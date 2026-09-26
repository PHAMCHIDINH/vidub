package store

import (
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"
)

type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)

// StoredJob is a Job as the UI sees it. The store hands out copies, so a
// template can read one while the pipeline updates the job.
type StoredJob struct {
	VideoID     string     `json:"video_id"`
	URL         string     `json:"url"`
	Mode        string     `json:"mode"`
	Voice       string     `json:"voice"`
	Status      JobStatus  `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// ErrJobRunning is returned when an action needs the job to be idle.
var ErrJobRunning = errors.New("job is running")

var jobIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidJobID reports whether id is a YouTube video ID. Job IDs are video IDs
// and name the job's directory, so nothing else may reach the file system.
func ValidJobID(id string) bool {
	return jobIDPattern.MatchString(id)
}

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*StoredJob
}

func NewJobStore() *JobStore {
	return &JobStore{jobs: make(map[string]*StoredJob)}
}

// Start registers a new run of a video. When that video is already running,
// nothing changes and it returns the running job with started = false, so
// two pipelines never share one work directory.
func (s *JobStore) Start(videoID, url, mode, voice string) (job StoredJob, started bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[videoID]; ok && j.Status == JobRunning {
		return *j, false
	}
	j := &StoredJob{
		VideoID:   videoID,
		URL:       url,
		Mode:      mode,
		Voice:     voice,
		Status:    JobRunning,
		CreatedAt: time.Now(),
	}
	s.jobs[videoID] = j
	return *j, true
}

// Restore adds a job found on disk at startup. A job already in the store wins.
func (s *JobStore) Restore(job StoredJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.VideoID]; !ok {
		s.jobs[job.VideoID] = &job
	}
}

func (s *JobStore) Get(videoID string) (StoredJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[videoID]
	if !ok {
		return StoredJob{}, false
	}
	return *j, true
}

// SetStatus updates a job and returns the updated copy.
func (s *JobStore) SetStatus(videoID string, status JobStatus) (StoredJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[videoID]
	if !ok {
		return StoredJob{}, false
	}
	j.Status = status
	if status == JobCompleted || status == JobFailed {
		now := time.Now()
		j.CompletedAt = &now
	}
	return *j, true
}

// Remove deletes a job. The remove callback (deleting its files) runs while
// the store is locked, so the job cannot be started again halfway through.
// It returns ErrJobRunning, without calling remove, for a running job.
// Remove also works for jobs the store does not know.
func (s *JobStore) Remove(videoID string, remove func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[videoID]; ok && j.Status == JobRunning {
		return ErrJobRunning
	}
	if err := remove(); err != nil {
		return err
	}
	delete(s.jobs, videoID)
	return nil
}

// List returns copies of all jobs, newest first.
func (s *JobStore) List() []StoredJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]StoredJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, *j)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}
