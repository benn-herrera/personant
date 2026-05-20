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
	Anchors []string // final anchor set (4–8)
}

// Curator drafts the closure artifacts for a retiring thread.
type Curator interface {
	DraftClosure(ctx context.Context, thread memops.Thread) (ClosureDraft, error)
}

// closureAnchorMin / closureAnchorMax bound the deterministic anchor
// selection (spec §2.2 hard range [4, 8]).
const (
	closureAnchorMin = 4
	closureAnchorMax = 8
)

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
		Anchors: SelectAnchors(thread.Frontmatter),
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
	// Fallback: drain a stream if Consult is unsupported by the client.
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
// FirstSeenTurn ascending (older first), take up to closureAnchorMax,
// at least closureAnchorMin if available. If HistorySymbols is too thin
// to yield closureAnchorMin entries, fall back to fm.Anchors.
//
// Exported so the deterministic selection can be unit-tested without a
// live model.
func SelectAnchors(fm memops.ThreadMeta) []string {
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
	if len(out) < closureAnchorMin {
		// History symbols too thin — fall back to the thread's existing
		// anchors, which the spine already holds in the [4,8] range.
		return append([]string(nil), fm.Anchors...)
	}
	return out
}
