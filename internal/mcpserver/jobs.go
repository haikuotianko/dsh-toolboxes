package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/haikuotianko/dsh-toolboxes/internal/catalog"
	"github.com/haikuotianko/dsh-toolboxes/internal/install"
)

// JobView is one in-flight or finished install, shaped for polling clients.
type JobView struct {
	ID        string `json:"id"`
	Tool      string `json:"tool"`
	Phase     string `json:"phase"` // queued | download | extract | probe | done | failed | cancelled
	Source    string `json:"source,omitempty"`
	Received  int64  `json:"received"`
	Total     int64  `json:"total"`
	SpeedBps  int64  `json:"speedBps"`
	Attempts  int    `json:"attempts"`
	Error     string `json:"error,omitempty"`
	Result    *InstallOutput `json:"result,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type installJob struct {
	mu      sync.Mutex
	view    JobView
	cancel  context.CancelFunc
	lastAt  time.Time
	lastGot int64
}

func (j *installJob) update(phase string, received, total int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if phase != "" {
		j.view.Phase = phase
	}
	if total > 0 {
		j.view.Total = total
	}
	if received > 0 {
		j.view.Received = received
	}
	now := time.Now()
	if !j.lastAt.IsZero() {
		if elapsed := now.Sub(j.lastAt).Seconds(); elapsed > 0.3 {
			j.view.SpeedBps = int64(float64(received-j.lastGot) / elapsed)
			j.lastGot = received
			j.lastAt = now
		}
	} else {
		j.lastAt = now
		j.lastGot = received
	}
	j.view.UpdatedAt = now
}

func (j *installJob) snapshot() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.view
}

// jobs is the per-service install registry.
type jobs struct {
	mu   sync.Mutex
	byID map[string]*installJob
}

func newJobs() *jobs { return &jobs{byID: map[string]*installJob{}} }

func randomID() string {
	buffer := make([]byte, 6)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

// StartInstall begins an install in the background and returns its job id.
// The MCP tool and the page share this path; a cancelled job keeps its partial
// download so the next attempt resumes.
func (s *Service) StartInstall(tool, url, archive string) (string, error) {
	id := randomID()
	ctx, cancel := context.WithCancel(context.Background())
	job := &installJob{
		cancel: cancel,
		view: JobView{
			ID: id, Tool: tool, Phase: "queued", Attempts: 1,
			StartedAt: time.Now(), UpdatedAt: time.Now(),
		},
	}
	s.jobRegistry.mu.Lock()
	s.jobRegistry.byID[id] = job
	s.jobRegistry.mu.Unlock()

	progress := func(phase string, received, total int64) { job.update(phase, received, total) }
	go func() {
		defer cancel()
		var (
			result install.Result
			err    error
		)
		if url != "" {
			result, err = install.InstallCustomWithProgress(ctx, s.root, tool, url, archive, progress)
		} else {
			cat, loadErr := catalog.Load(s.root)
			if loadErr != nil {
				err = loadErr
			} else if entry, ok := cat.Find(tool); !ok {
				err = fmt.Errorf("未知工具 %q；toolbox_list 可以看可用清单", tool)
			} else {
				result, err = install.InstallToolWithProgress(ctx, s.root, entry, progress)
			}
		}
		job.mu.Lock()
		defer job.mu.Unlock()
		job.view.UpdatedAt = time.Now()
		job.view.Attempts = max(result.Attempts, 1)
		if err != nil {
			if ctx.Err() != nil {
				job.view.Phase = "cancelled"
				job.view.Error = "已取消；已下载的部分会保留，重试可续传"
			} else {
				job.view.Phase = "failed"
				job.view.Error = err.Error()
			}
			return
		}
		job.view.Phase = "done"
		job.view.Result = &InstallOutput{
			Tool: result.Manifest.ID, Version: result.Manifest.Version,
			Binary: result.Manifest.Command, Source: result.Manifest.Source,
			Attempts: result.Attempts,
		}
		job.view.Source = result.Manifest.Source
		if result.Manifest.SizeBytes > 0 {
			job.view.Total = result.Manifest.SizeBytes
			job.view.Received = result.Manifest.SizeBytes
		}
	}()
	return id, nil
}

// JobSnapshot returns one job's current state.
func (s *Service) JobSnapshot(id string) (JobView, bool) {
	s.jobRegistry.mu.Lock()
	job, ok := s.jobRegistry.byID[id]
	s.jobRegistry.mu.Unlock()
	if !ok {
		return JobView{}, false
	}
	return job.snapshot(), true
}

// CancelInstall stops a running job; its .part file survives for resume.
func (s *Service) CancelInstall(id string) bool {
	s.jobRegistry.mu.Lock()
	job, ok := s.jobRegistry.byID[id]
	s.jobRegistry.mu.Unlock()
	if !ok {
		return false
	}
	job.cancel()
	return true
}

// WaitInstall blocks until the job settles (used by the MCP tool, so the agent
// still receives one final result while the page polls).
func (s *Service) WaitInstall(ctx context.Context, id string) (JobView, error) {
	for {
		view, ok := s.JobSnapshot(id)
		if !ok {
			return JobView{}, fmt.Errorf("unknown install job %q", id)
		}
		switch view.Phase {
		case "done", "failed", "cancelled":
			if view.Error != "" {
				return view, fmt.Errorf("%s", view.Error)
			}
			return view, nil
		}
		select {
		case <-ctx.Done():
			s.CancelInstall(id)
			return view, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
