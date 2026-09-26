package store

import (
	"errors"
	"testing"
)

const id = "dQw4w9WgXcQ"

func TestStartRefusesSecondRunWhileRunning(t *testing.T) {
	s := NewJobStore()
	if _, started := s.Start(id, "u", "voiceover", "v"); !started {
		t.Fatal("first start refused")
	}

	running, started := s.Start(id, "u", "replace", "v")
	if started {
		t.Fatal("second start of a running video was accepted")
	}
	if running.Mode != "voiceover" {
		t.Errorf("got mode %q, want the running job (voiceover)", running.Mode)
	}

	s.SetStatus(id, JobFailed)
	if _, started := s.Start(id, "u", "replace", "v"); !started {
		t.Error("re-run after the job finished was refused")
	}
}

func TestRemoveSkipsRunningJob(t *testing.T) {
	s := NewJobStore()
	s.Start(id, "u", "voiceover", "v")

	called := false
	remove := func() error { called = true; return nil }

	if err := s.Remove(id, remove); !errors.Is(err, ErrJobRunning) || called {
		t.Fatalf("err = %v, remove called = %v; want ErrJobRunning and no call", err, called)
	}

	s.SetStatus(id, JobCompleted)
	if err := s.Remove(id, remove); err != nil || !called {
		t.Fatalf("err = %v, remove called = %v; want success", err, called)
	}
	if _, ok := s.Get(id); ok {
		t.Error("removed job is still in the store")
	}
}

func TestRemoveKeepsJobWhenDeleteFails(t *testing.T) {
	s := NewJobStore()
	s.Restore(StoredJob{VideoID: id, Status: JobCompleted})

	if err := s.Remove(id, func() error { return errors.New("disk busy") }); err == nil {
		t.Fatal("expected the remove error")
	}
	if _, ok := s.Get(id); !ok {
		t.Error("job left the store although its files were not deleted")
	}
}

func TestGetReturnsACopy(t *testing.T) {
	s := NewJobStore()
	s.Start(id, "u", "voiceover", "v")

	j, _ := s.Get(id)
	j.Status = JobFailed

	if again, _ := s.Get(id); again.Status != JobRunning {
		t.Errorf("changing a returned job changed the store: status %q", again.Status)
	}
}
