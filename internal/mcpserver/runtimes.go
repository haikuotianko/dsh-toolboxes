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
	Cached    bool             `json:"cached"`
	Runtimes  []runtimes.Entry `json:"runtimes"`
}

// Runtimes reports the interpreters and compilers this machine can actually
// execute, with the version found. The probe is cached in <root>/runtimes.json
// so a second window (or a second process) does not pay for it again; refresh
// forces a fresh probe when the user just installed something.
func (s *Service) Runtimes(ctx context.Context, refresh bool) (RuntimesOutput, error) {
	snapshot, cached, err := runtimes.Get(ctx, s.root, runtimes.DefaultTTL, refresh)
	if err != nil {
		return RuntimesOutput{}, err
	}
	return RuntimesOutput{
		Host:      snapshot.Host,
		CheckedAt: snapshot.CheckedAt,
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

// AddRuntime registers a language the built-in probe list does not know.
// command may be a bare name ("rust") or a path; ext enables script archiving.
func (s *Service) AddRuntime(ctx context.Context, id, name, command, ext string) (runtimes.Snapshot, error) {
	snapshot, err := runtimes.AddCustom(ctx, s.root, id, name, command, ext)
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
