package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/haikuotianko/dsh-toolboxes/internal/install"
	"github.com/haikuotianko/dsh-toolboxes/internal/runlog"
)

// Where a run came from, so the page can tell an agent call from its own click.
const (
	OriginAgent = "agent"
	OriginPage  = "page"
)

// RunView is one run as the page lists it.
type RunView struct {
	runlog.Record
	ElapsedMs int64 `json:"elapsedMs"`
}

// RunLogView is one run with the tail of its console output.
type RunLogView struct {
	RunView
	Output string `json:"output"`
	// Partial means the returned text starts mid-log; the page shows a marker.
	Partial bool `json:"partial"`
}

// Run executes an installed tool and mirrors its output into <root>/runs, so a
// page served by another process can watch the same command live.
func (s *Service) Run(ctx context.Context, tool string, args []string, cwd string, timeoutMs int, origin string) (RunOutput, error) {
	// A broken log directory must never block the command itself.
	writer, logErr := runlog.Start(s.root, tool, args, cwd, origin)
	if logErr != nil {
		s.logf("run log: %v", logErr)
	}
	var hook *install.RunHook
	if writer != nil {
		hook = &install.RunHook{
			Started: writer.SetPID,
			Output:  func(_ string, chunk []byte) { _, _ = writer.Write(chunk) },
		}
	}
	out, runErr := install.Run(ctx, s.root, tool, args, cwd, time.Duration(timeoutMs)*time.Millisecond, hook)
	if writer != nil {
		phase, message := "done", ""
		switch {
		case runErr == nil:
		case errors.Is(runErr, context.Canceled):
			phase, message = "cancelled", "已取消"
		case errors.Is(runErr, context.DeadlineExceeded):
			phase, message = "timeout", runErr.Error()
		default:
			phase, message = "failed", runErr.Error()
		}
		writer.Finish(phase, out.ExitCode, message)
	}
	return RunOutput{Tool: tool, ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr, Truncated: out.Truncated}, runErr
}

// Runs lists recent runs (newest first) for the page's live panel.
func (s *Service) Runs() ([]RunView, error) {
	records, err := runlog.List(s.root)
	if err != nil {
		return nil, err
	}
	views := make([]RunView, 0, len(records))
	for _, record := range records {
		// Expired rows only disappear from the list; prune deletes the files.
		if !record.Running() && record.EndedAt != nil && time.Since(*record.EndedAt) > runlog.FinishedTTL {
			continue
		}
		views = append(views, RunView{Record: record, ElapsedMs: elapsed(record)})
	}
	return views, nil
}

// RunLog returns one run plus the tail of its console output.
func (s *Service) RunLog(id string, tailBytes int) (RunLogView, error) {
	record, err := runlog.Load(s.root, id)
	if err != nil {
		return RunLogView{}, err
	}
	if record == nil {
		return RunLogView{}, fmt.Errorf("没有这个运行记录：%s", id)
	}
	normalized := record.Normalize()
	text, partial, err := runlog.Tail(s.root, id, tailBytes)
	if err != nil {
		return RunLogView{}, err
	}
	return RunLogView{
		RunView: RunView{Record: normalized, ElapsedMs: elapsed(normalized)},
		Output:  text,
		Partial: partial,
	}, nil
}

// CancelRun stops a run started by this or any other toolbox process.
func (s *Service) CancelRun(id string) error { return runlog.Cancel(s.root, id) }

// ClearRuns drops every settled record; running ones stay.
func (s *Service) ClearRuns() (int, error) { return runlog.ClearFinished(s.root) }

func elapsed(record runlog.Record) int64 {
	end := time.Now()
	if record.EndedAt != nil {
		end = *record.EndedAt
	}
	if record.StartedAt.IsZero() {
		return 0
	}
	if span := end.Sub(record.StartedAt); span > 0 {
		return span.Milliseconds()
	}
	return 0
}
