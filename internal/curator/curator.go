// Package curator drafts the closure artifacts for a retiring thread
// (spec §3.5 / §5.2): a short faithful summary of the thread's
// operational content plus a final anchor set.
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
	"sort"
	"strings"

	"personant/internal/memops"
	"personant/internal/model"
)

// ClosureDraft is the curator's output for a closing thread.
type ClosureDraft struct {
	Summary string   // 100–150 char gist of the thread's operational content (spec §5.2)
	Anchors []string // final anchor set (0..AnchorProjectionMax — no floor; the projection owns the ceiling)
}

// Curator drafts the closure artifacts for a retiring thread.
type Curator interface {
	DraftClosure(ctx context.Context, thread memops.Thread) (ClosureDraft, error)
}

// closureAnchorMax bounds the deterministic anchor selection at
// retirement. It aliases the §2.2 projection ceiling
// memops.AnchorProjectionMax so the curator's selection respects the
// single port-level invariant rather than restating it locally. There is
// no minimum: the 4-floor is deleted (anchor-lifecycle Inc 1), so a thin
// thread legitimately yields fewer than the ceiling.
const closureAnchorMax = memops.AnchorProjectionMax

// HTTPCurator is the model-backed Curator. The summary is drafted by
// the LLM; the anchors are selected deterministically from the
// thread's history symbols (the model is never asked for anchors —
// §5.2 keeps anchor selection mechanical so it stays auditable).
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

// DraftClosure asks the model for a faithful gist of thread.Body and
// selects the final anchor set deterministically from the thread's
// history symbols.
func (c *HTTPCurator) DraftClosure(ctx context.Context, thread memops.Thread) (ClosureDraft, error) {
	if c.client == nil {
		return ClosureDraft{}, errors.New("curator: nil model client")
	}
	req := model.DefaultRequest(c.model, []model.Message{
		{Role: "system", Content: curatorPrompt},
		{Role: "user", Content: thread.Body},
	})
	summary, err := c.consult(ctx, req)
	if err != nil {
		return ClosureDraft{}, fmt.Errorf("curator: draft summary: %w", err)
	}
	return ClosureDraft{
		Summary: strings.TrimSpace(summary),
		Anchors: SelectAnchors(thread.Meta),
	}, nil
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

// SelectAnchors picks the final anchor set for a closing thread from
// fm.HistorySymbols: rank by Count descending, tie-break by
// FirstSeenTurn ascending (older first), take up to closureAnchorMax.
// When HistorySymbols is empty (no symbol history to project from), fall
// back to the thread's existing fm.Anchors. There is no minimum floor —
// the 4-floor is deleted (anchor-lifecycle Inc 1), so a thin history
// legitimately yields fewer than the ceiling.
//
// Exported so the deterministic selection can be unit-tested without a
// live model.
func SelectAnchors(fm memops.ThreadMeta) []string {
	if len(fm.HistorySymbols) == 0 {
		// No symbol history to project from — fall back to whatever
		// anchors the thread already holds.
		return append([]string(nil), fm.Anchors...)
	}

	syms := append([]memops.HistorySymbol(nil), fm.HistorySymbols...)
	sort.SliceStable(syms, func(i, j int) bool {
		if syms[i].Count != syms[j].Count {
			return syms[i].Count > syms[j].Count
		}
		if syms[i].FirstSeenTurn != syms[j].FirstSeenTurn {
			return syms[i].FirstSeenTurn < syms[j].FirstSeenTurn
		}
		return syms[i].Normalized < syms[j].Normalized
	})

	out := make([]string, 0, closureAnchorMax)
	for _, s := range syms {
		if s.Normalized == "" {
			continue
		}
		out = append(out, s.Normalized)
		if len(out) == closureAnchorMax {
			break
		}
	}
	return out
}
