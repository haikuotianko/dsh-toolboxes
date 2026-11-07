package install

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/haikuotianko/dsh-toolboxes/internal/catalog"
)

// SourceSpeed is one measured download origin.
type SourceSpeed struct {
	URL     string `json:"url"`
	Archive string `json:"archive,omitempty"`
	Bytes   int64  `json:"bytes"`
	Bps     int64  `json:"bps"`
	Error   string `json:"error,omitempty"`
}

// probeWindow is how much of a source we pull to estimate its speed.
const probeWindow = 512 * 1024

// ProbeSources measures each candidate by fetching a bounded window from it, so
// a slow mirror can be skipped instead of costing the whole download. Best
// effort: a source that does not support Range is timed on what it returns.
func ProbeSources(ctx context.Context, sources []catalog.Source) []SourceSpeed {
	out := make([]SourceSpeed, 0, len(sources))
	for _, source := range sources {
		out = append(out, probeOne(ctx, source))
	}
	return out
}

func probeOne(ctx context.Context, source catalog.Source) SourceSpeed {
	measured := SourceSpeed{URL: source.URL, Archive: source.Archive}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, source.URL, nil)
	if err != nil {
		measured.Error = err.Error()
		return measured
	}
	request.Header.Set("User-Agent", "toolbox/0.1")
	request.Header.Set("Range", fmt.Sprintf("bytes=0-%d", probeWindow-1))
	started := time.Now()
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		measured.Error = err.Error()
		return measured
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		measured.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
		return measured
	}
	written, err := io.CopyN(io.Discard, response.Body, probeWindow)
	if err != nil && err != io.EOF {
		measured.Bytes = written
		measured.Error = err.Error()
		return measured
	}
	elapsed := time.Since(started).Seconds()
	measured.Bytes = written
	if elapsed > 0 {
		measured.Bps = int64(float64(written) / elapsed)
	}
	return measured
}

// OrderBySpeed puts the fastest reachable source first, keeping the catalog
// order for anything that failed to probe or probed as zero.
func OrderBySpeed(sources []catalog.Source, speeds []SourceSpeed) []catalog.Source {
	if len(sources) < 2 {
		return sources
	}
	type ranked struct {
		source catalog.Source
		bps    int64
		index  int
	}
	ranks := make([]ranked, 0, len(sources))
	for index, source := range sources {
		var bps int64
		for _, speed := range speeds {
			if speed.URL == source.URL && speed.Error == "" {
				bps = speed.Bps
			}
		}
		ranks = append(ranks, ranked{source: source, bps: bps, index: index})
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		if ranks[i].bps == ranks[j].bps {
			return ranks[i].index < ranks[j].index
		}
		return ranks[i].bps > ranks[j].bps
	})
	out := make([]catalog.Source, len(ranks))
	for i, rank := range ranks {
		out[i] = rank.source
	}
	return out
}
