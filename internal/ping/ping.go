// Package ping implements `personant ping`: the smallest end-to-end
// exercise of the connectivity stack. Loads providers, sends one chat
// completion, prints the result.
package ping

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"personant/internal/model"
	"personant/internal/store"
)

// Options carries the ping-time knobs from the CLI flag layer.
type Options struct {
	Provider string        // provider name from providers.toml
	Prompt   string        // user prompt to send
	Model    string        // override the provider's default model; empty = use default
	Timeout  time.Duration // request timeout; 0 → 30s
	Stdout   io.Writer     // body output sink
	Stderr   io.Writer     // summary line + warnings sink
}

// Run loads providers from paths, picks one, and performs one
// chat-completion round-trip. Stdout receives the response body verbatim;
// Stderr receives a one-line summary on success or a clear error message
// on failure.
func Run(paths store.PersonantPaths, opts Options) error {
	if opts.Stdout == nil || opts.Stderr == nil {
		return fmt.Errorf("ping: Stdout/Stderr are required")
	}
	if opts.Provider == "" {
		return fmt.Errorf("ping: Provider name is required")
	}
	if opts.Prompt == "" {
		return fmt.Errorf("ping: Prompt is required")
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	providers, err := store.LoadProviders(paths.Providers)
	if err != nil {
		return fmt.Errorf("ping: load providers: %w", err)
	}
	if len(providers) == 0 {
		return fmt.Errorf("ping: no providers configured in %s; edit it to add one (see spec §8.2.1)", paths.Providers)
	}
	provider, ok := providers.Get(opts.Provider)
	if !ok {
		return fmt.Errorf("ping: provider %q not found in %s", opts.Provider, paths.Providers)
	}

	chosenModel := opts.Model
	if chosenModel == "" {
		chosenModel = provider.DefaultModel
	}
	if chosenModel == "" {
		return fmt.Errorf("ping: no model specified and provider %q has no defaultModel", opts.Provider)
	}

	client := model.NewHTTPClient(provider)
	return runWithClient(client, opts, chosenModel, timeout)
}

// runWithClient is the model-client-agnostic body of Run. Split out so
// tests can drive a MockClient without going through providers.toml.
func runWithClient(client model.Client, opts Options, chosenModel string, timeout time.Duration) error {
	req := model.DefaultRequest(chosenModel, []model.Message{
		{Role: "user", Content: opts.Prompt},
	})

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	sr, err := client.ConsultStream(ctx, req)
	if err != nil {
		return fmt.Errorf("ping: consult: %w", err)
	}
	defer sr.Close()

	// ping is a free-form connectivity probe — no topic-tag stripping.
	// Stream chunks straight to stdout as they arrive.
	for {
		chunk, err := sr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("ping: stream: %w", err)
		}
		if chunk.Content == "" {
			continue
		}
		if _, werr := io.WriteString(opts.Stdout, chunk.Content); werr != nil {
			return fmt.Errorf("ping: write stdout: %w", werr)
		}
	}
	final := sr.Final()
	elapsed := time.Since(start)

	// Cap with a newline so the summary on stderr lands on a fresh line.
	if final.Content == "" || final.Content[len(final.Content)-1] != '\n' {
		if _, err := io.WriteString(opts.Stdout, "\n"); err != nil {
			return fmt.Errorf("ping: write stdout: %w", err)
		}
	}

	fmt.Fprintf(opts.Stderr, "ping ok: %s %s tokens=%d/%d/%d elapsed=%s\n",
		opts.Provider, chosenModel,
		final.Usage.PromptTokens, final.Usage.CompletionTokens, final.Usage.TotalTokens,
		elapsed.Round(time.Millisecond))
	return nil
}
