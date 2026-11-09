package mcpserver

import (
	"context"
	"time"

	"github.com/haikuotianko/dsh-toolboxes/internal/runtimes"
)

// RuntimesOutput is the language / compiler inventory of this machine.
type RuntimesOutput struct {
	Host      string           `json:"host"`
	CheckedAt time.Time        `json:"checkedAt"`
	Mode      string           `json:"mode"`
	Cached    bool             `json:"cached"`
	Runtimes  []runtimes.Entry `json:"runtimes"`
}

// Runtimes reports the interpreters and compilers this machine can execute.
//
// Discovery is environment-only (PATH and *_HOME / *_ROOT variables) and starts
// no process, so it is cheap and side-effect free; versions come from those
// variables or from a version-looking directory in the install path. Pass exec
// to additionally run one `<cmd> --version` per runtime for exact versions.
//
// The result is cached in <root>/runtimes.json so another window (or another
// process) does not pay for it again.
func (s *Service) Runtimes(ctx context.Context, refresh, exec bool) (RuntimesOutput, error) {
	snapshot, cached, err := runtimes.Get(ctx, s.root, runtimes.DefaultTTL, refresh, exec)
	if err != nil {
		return RuntimesOutput{}, err
	}
	return RuntimesOutput{
		Host:      snapshot.Host,
		CheckedAt: snapshot.CheckedAt,
		Mode:      snapshot.Mode,
		Cached:    cached,
		Runtimes:  snapshot.Entries,
	}, nil
}

// RuntimesSnapshot returns the cached inventory without probing.
func (s *Service) RuntimesSnapshot() (runtimes.Snapshot, error) {
	snapshot, err := runtimes.Load(s.root)
	if err != nil {
		return runtimes.Snapshot{}, err
	}
	if snapshot == nil {
		return runtimes.Snapshot{}, nil
	}
	return *snapshot, nil
}

// AddRuntime registers a language the built-in list does not know.
func (s *Service) AddRuntime(ctx context.Context, id, name, command, ext, version string, exec bool) (runtimes.Snapshot, error) {
	snapshot, err := runtimes.AddCustom(ctx, s.root, id, name, command, ext, version, exec)
	if err != nil {
		return runtimes.Snapshot{}, err
	}
	s.logf("runtime added: %s -> %s (ext %q)", id, command, ext)
	return snapshot, nil
}

// RemoveRuntime deletes a user-added language row.
func (s *Service) RemoveRuntime(id string) (runtimes.Snapshot, error) {
	return runtimes.RemoveCustom(s.root, id)
}
