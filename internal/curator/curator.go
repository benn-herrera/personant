// Package curator drafts the closing summary for a retiring thread
// (spec §3.5 / §5.2): a short faithful gist of the thread's operational
// content.
//
// The package mirrors internal/recall's shape — an interface the
// experience layer depends on, plus a concrete model-backed
// implementation. It must not import internal/turn (turn imports
// curator; the dependency arrow runs turn → curator).
package curator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"personant/internal/memops"
	"personant/internal/model"
)

// Curator drafts the closing summary for a retiring thread: a 100–150
// char gist of its operational content (spec §5.2).
type Curator interface {
	DraftClosure(ctx context.Context, thread memops.Thread) (string, error)
}

// HTTPCurator is the model-backed Curator. The model is never asked for
// anchors: a closing thread's anchors come from the deterministic
// projection.
type HTTPCurator struct {
	client model.Client
	model  string
}

// NewHTTPCurator constructs an HTTPCurator backed by client, calling
// model for the summary draft.
func NewHTTPCurator(client model.Client, model string) Curator {
	return &HTTPCurator{client: client, model: model}
}

// curatorPrompt is the §5.2 instruction: produce a short gist faithful
// to the thread's operational body. The model receives the body as the
// user message; this is the system framing.
const curatorPrompt = `You are summarizing a now-idle work thread for archival.
Write a single faithful gist of the thread's operational content — what was
worked on and where it landed. Target 100 to 150 characters. Output only the
gist, no preamble, no quotes, no markdown.`

// DraftClosure asks the model for a faithful gist of thread.Body.
func (c *HTTPCurator) DraftClosure(ctx context.Context, thread memops.Thread) (string, error) {
	if c.client == nil {
		return "", errors.New("curator: nil model client")
	}
	req := model.DefaultRequest(c.model, []model.Message{
		{Role: "system", Content: curatorPrompt},
		{Role: "user", Content: thread.Body},
	})
	summary, err := c.consult(ctx, req)
	if err != nil {
		return "", fmt.Errorf("curator: draft summary: %w", err)
	}
	return strings.TrimSpace(summary), nil
}

// consult runs one round-trip. Consult is the blocking flavor and the
// natural fit for a programmatic draft; if a deployment ever ships a
// stream-only Client, draining ConsultStream is the fallback. The
// model.Client interface exposes Consult, so use it directly.
func (c *HTTPCurator) consult(ctx context.Context, req model.Request) (string, error) {
	resp, err := c.client.Consult(ctx, req)
	if err == nil {
		return resp.Content, nil
	}
	// The stream fallback exists ONLY for a client that genuinely can't
	// serve a blocking Consult (signaled by model.ErrConsultUnsupported).
	// Every other Consult error — transient network, HTTP status, decode —
	// is a real failure; retrying it via ConsultStream would double the
	// cost on a flake, so propagate it unchanged.
	if !errors.Is(err, model.ErrConsultUnsupported) {
		return "", err
	}
	// Fallback: drain a stream because blocking Consult is unsupported.
	sr, serr := c.client.ConsultStream(ctx, req)
	if serr != nil {
		return "", err
	}
	defer sr.Close()
	for {
		if _, nerr := sr.Next(); errors.Is(nerr, io.EOF) {
			break
		} else if nerr != nil {
			return "", nerr
		}
	}
	return sr.Final().Content, nil
}
