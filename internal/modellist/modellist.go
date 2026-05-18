// Package modellist implements `personant models`: list the models
// exposed by a configured provider. Loads providers, looks up the named
// one, calls its /models endpoint, prints the IDs in sorted order.
package modellist

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"personant/internal/clock"
	"personant/internal/memops"
	"personant/internal/model"
)

// Options carries the call-time knobs from the CLI flag layer.
type Options struct {
	Provider string        // provider name from providers.toml; "" → "local"
	Timeout  time.Duration // request timeout; 0 → 30s
	Stdout   io.Writer     // model-id sink (one per line, sorted ascending)
	Stderr   io.Writer     // summary line + warnings sink
}

// Run loads providers via ops, picks one, fetches its model list, and
// prints one model id per line to opts.Stdout (sorted ascending). A
// one-line summary goes to opts.Stderr.
func Run(ops memops.MemoryOps, opts Options) error {
	if opts.Stdout == nil || opts.Stderr == nil {
		return fmt.Errorf("models: Stdout/Stderr are required")
	}
	provider := opts.Provider
	if provider == "" {
		provider = "local"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	providers, _, err := ops.LoadProviders(context.Background())
	if err != nil {
		return fmt.Errorf("models: load providers: %w", err)
	}
	p, ok := providers[provider]
	if !ok {
		return fmt.Errorf("models: provider %q not found in providers.toml", provider)
	}

	client := model.NewHTTPClient(p)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := clock.Profiling()
	infos, err := client.ListModels(ctx)
	elapsed := clock.Since(start)
	if err != nil {
		return fmt.Errorf("models: list: %w", err)
	}

	ids := make([]string, 0, len(infos))
	for _, mi := range infos {
		ids = append(ids, mi.ID)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if _, err := fmt.Fprintln(opts.Stdout, id); err != nil {
			return fmt.Errorf("models: write stdout: %w", err)
		}
	}
	fmt.Fprintf(opts.Stderr, "models ok: %s count=%d elapsed=%s\n",
		provider, len(ids), elapsed.Round(time.Millisecond))
	return nil
}
