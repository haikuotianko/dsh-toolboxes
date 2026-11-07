// Package runlog keeps a cross-process record of the commands the toolbox runs.
//
// The MCP server the agent talks to is a different process from the HTTP server
// behind the manager page, and either may run commands. Rows therefore live on
// disk under <root>/runs:
//
//	runs/<id>.json  metadata: tool, args, phase, pid, byte count, timestamps
//	runs/<id>.log   raw stdout+stderr, appended while the command runs
//
// A running record is refreshed every couple of seconds, so a GUI in another
// process can tell a quiet command (a long ffmpeg encode) from a writer that
// vanished (crash, kill) and reports the latter as "lost" instead of running
// forever.
package runlog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// MaxLogBytes caps one run's log file: a runaway command stops growing it,
	// the metadata keeps counting so the page can say it was cut.
	MaxLogBytes = 4 << 20
	// Heartbeat is how often a running record is rewritten (liveness marker).
	Heartbeat = 2 * time.Second
	// StaleAfter marks a record whose writer vanished by the age of that marker.
	StaleAfter = 20 * time.Second
	// FinishedTTL is how long a finished record stays on disk.
	FinishedTTL = 30 * time.Minute
	// MaxRecords is the directory budget (running records are never dropped).
	MaxRecords = 40
	// flushGap keeps hot streams from rewriting the JSON on every chunk.
	flushGap = 400 * time.Millisecond
)

// Record is one command the toolbox ran, in flight or finished.
type Record struct {
	ID        string     `json:"id"`
	Tool      string     `json:"tool"`
	Args      []string   `json:"args,omitempty"`
	Cwd       string     `json:"cwd,omitempty"`
	Origin    string     `json:"origin,omitempty"` // agent | page
	PID       int        `json:"pid,omitempty"`
	Phase     string     `json:"phase"` // running | done | failed | timeout | cancelled | lost
	ExitCode  int        `json:"exitCode"`
	Error     string     `json:"error,omitempty"`
	Bytes     int64      `json:"bytes"`
	Truncated bool       `json:"truncated,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// Running reports whether the command is still in flight.
func (r Record) Running() bool { return r.Phase == "running" }

// Normalize marks a record whose writer vanished as lost, so a page never shows
// a dead command as running forever.
func (r Record) Normalize() Record {
	if r.Running() && time.Since(r.UpdatedAt) > StaleAfter {
		r.Phase = "lost"
		if r.Error == "" {
			r.Error = "进程已退出，控制台内容到此为止"
		}
	}
	return r
}

// Dir is <root>/runs.
func Dir(root string) string { return filepath.Join(root, "runs") }

func jsonPath(root, id string) string   { return filepath.Join(Dir(root), id+".json") }
func logPath(root, id string) string    { return filepath.Join(Dir(root), id+".log") }
func cancelPath(root, id string) string { return filepath.Join(Dir(root), id+".cancel") }

// Writer appends one run's output and keeps its metadata fresh.
type Writer struct {
	mu        sync.Mutex
	root      string
	rec       Record
	file      *os.File
	written   int64
	lastFlush time.Time
	closed    bool
	stop      chan struct{}
	once      sync.Once
}

// Start opens a record for a command that is about to run.
func Start(root, tool string, args []string, cwd, origin string) (*Writer, error) {
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	prune(root)
	now := time.Now()
	rec := Record{
		ID: newID(now), Tool: tool, Args: args, Cwd: cwd, Origin: origin,
		Phase: "running", StartedAt: now, UpdatedAt: now,
	}
	file, err := os.OpenFile(logPath(root, rec.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := &Writer{root: root, rec: rec, file: file, lastFlush: now, stop: make(chan struct{})}
	w.mu.Lock()
	flushErr := w.flushLocked()
	w.mu.Unlock()
	if flushErr != nil {
		file.Close()
		return nil, flushErr
	}
	go w.beat()
	return w, nil
}

// ID is the record id, usable from another process through Tail and Cancel.
func (w *Writer) ID() string { return w.rec.ID }

// SetPID records the process id once the command started, so the page can tell
// a running command from a dead writer and offer a cancel button.
func (w *Writer) SetPID(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || pid <= 0 {
		return
	}
	w.rec.PID = pid
	_ = w.flushLocked()
}

// Write appends output and returns len(p): a broken log must never break the
// command whose output it is mirroring.
func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	w.rec.Bytes += int64(len(p))
	w.rec.UpdatedAt = now
	if w.file != nil {
		if room := int64(MaxLogBytes) - w.written; room > 0 {
			chunk := p
			if int64(len(chunk)) > room {
				chunk = chunk[:room]
			}
			if n, err := w.file.Write(chunk); err == nil {
				w.written += int64(n)
			}
		}
		if w.written >= MaxLogBytes {
			w.rec.Truncated = true
		}
	}
	if now.Sub(w.lastFlush) > flushGap {
		w.lastFlush = now
		_ = w.flushLocked()
	}
	return len(p), nil
}

// Finish settles the record: phase, exit code and (optional) error message.
func (w *Writer) Finish(phase string, exitCode int, errMsg string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	now := time.Now()
	// Another process can cancel a run; the marker tells the writer that the
	// exit it is about to record was requested by the user, not a failure.
	if _, statErr := os.Stat(cancelPath(w.root, w.rec.ID)); statErr == nil {
		phase = "cancelled"
		if errMsg == "" {
			errMsg = "已取消"
		}
		_ = os.Remove(cancelPath(w.root, w.rec.ID))
	}
	w.rec.Phase = phase
	w.rec.ExitCode = exitCode
	w.rec.Error = errMsg
	w.rec.UpdatedAt = now
	w.rec.EndedAt = &now
	_ = w.flushLocked()
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	w.once.Do(func() { close(w.stop) })
}

func (w *Writer) beat() {
	ticker := time.NewTicker(Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case now := <-ticker.C:
			w.mu.Lock()
			if !w.closed {
				w.rec.UpdatedAt = now
				_ = w.flushLocked()
			}
			w.mu.Unlock()
		}
	}
}

func (w *Writer) flushLocked() error {
	raw, err := json.MarshalIndent(w.rec, "", "  ")
	if err != nil {
		return err
	}
	target := jsonPath(w.root, w.rec.ID)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(temp, target)
}

// List reads every record, newest first, after dropping expired ones. A record
// whose heartbeat stopped is reported as lost.
func List(root string) ([]Record, error) {
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Record, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		rec, err := Load(root, strings.TrimSuffix(name, ".json"))
		if err != nil || rec == nil {
			continue
		}
		out = append(out, rec.Normalize())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// Load reads one record; a missing record returns (nil, nil).
func Load(root, id string) (*Record, error) {
	if !validID(id) {
		return nil, fmt.Errorf("invalid run id: %q", id)
	}
	raw, err := os.ReadFile(jsonPath(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("parse run %s: %w", id, err)
	}
	return &rec, nil
}

// Tail returns the last max bytes of a run's log, trimmed to a whole line and a
// valid UTF-8 boundary. truncated reports that earlier output was dropped.
func Tail(root, id string, max int) (string, bool, error) {
	if !validID(id) {
		return "", false, fmt.Errorf("invalid run id: %q", id)
	}
	file, err := os.Open(logPath(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	size := info.Size()
	start := int64(0)
	if max > 0 && size > int64(max) {
		start = size - int64(max)
	}
	reader := io.NewSectionReader(file, start, size-start)
	buf, err := io.ReadAll(reader)
	if err != nil {
		return "", false, err
	}
	truncated := start > 0
	if truncated {
		// Drop the partial first line and any split rune at the seam.
		if cut := strings.IndexByte(string(buf), '\n'); cut >= 0 {
			buf = buf[cut+1:]
		} else {
			buf = nil
		}
	}
	for len(buf) > 0 && !utf8.Valid(buf) {
		buf = buf[1:]
	}
	return string(buf), truncated, nil
}

// Cancel stops a run started by any process: the record carries the pid.
func Cancel(root, id string) error {
	rec, err := Load(root, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("没有这个运行记录：%s", id)
	}
	if !rec.Running() {
		return fmt.Errorf("这条命令已经结束（%s）", rec.Phase)
	}
	if rec.PID <= 0 {
		return fmt.Errorf("命令还没真正启动，稍后再试")
	}
	// Drop the marker before the kill so the writer cannot settle the record
	// as a plain failure in between.
	if err := os.WriteFile(cancelPath(root, id), []byte(time.Now().Format(time.RFC3339)), 0o644); err != nil {
		return err
	}
	if err := killTree(rec.PID); err != nil {
		_ = os.Remove(cancelPath(root, id))
		return err
	}
	return nil
}

// ClearFinished deletes every settled record and returns how many went away.
func ClearFinished(root string) (int, error) {
	records, err := List(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, rec := range records {
		if rec.Running() {
			continue
		}
		if err := remove(root, rec.ID); err == nil {
			removed++
		}
	}
	return removed, nil
}

func killTree(pid int) error {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("结束进程 %d 失败：%v %s", pid, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func remove(root, id string) error {
	var firstErr error
	for _, path := range []string{jsonPath(root, id), logPath(root, id), cancelPath(root, id)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// prune keeps the directory bounded from whichever process starts a run.
func prune(root string) {
	records, err := List(root)
	if err != nil {
		return
	}
	now := time.Now()
	kept := 0
	for _, rec := range records {
		if rec.Running() {
			continue
		}
		expired := rec.EndedAt != nil && now.Sub(*rec.EndedAt) > FinishedTTL
		if expired || kept >= MaxRecords {
			_ = remove(root, rec.ID)
			continue
		}
		kept++
	}
	// Orphan logs and leftover temp files from a killed writer.
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".log"), strings.HasSuffix(name, ".cancel"):
			id := strings.TrimSuffix(strings.TrimSuffix(name, ".log"), ".cancel")
			if _, statErr := os.Stat(jsonPath(root, id)); os.IsNotExist(statErr) {
				_ = os.Remove(filepath.Join(Dir(root), name))
			}
		case strings.HasSuffix(name, ".tmp"):
			if info, statErr := entry.Info(); statErr == nil && now.Sub(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(Dir(root), name))
			}
		}
	}
}

func newID(now time.Time) string {
	suffix := fmt.Sprintf("%04x", now.UnixNano()&0xffff)
	return now.Format("20060102-150405") + "-" + suffix
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
