package curator

import (
	"context"
	"errors"
	"slices"
	"testing"

	"personant/internal/memops"
	"personant/internal/model"
)

// stubClient is a minimal model.Client for exercising the consult fallback
// arms. consultErr, when non-nil, is returned by Consult; ConsultStream
// records whether it was reached and, when streamCalled matters, hands back
// a scripted stream.
type stubClient struct {
	consultErr   error
	streamResp   model.Response
	streamCalled bool
}

func (s *stubClient) Consult(ctx context.Context, req model.Request) (model.Response, error) {
	if s.consultErr != nil {
		return model.Response{}, s.consultErr
	}
	return model.Response{Content: "blocking"}, nil
}

func (s *stubClient) ConsultStream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	s.streamCalled = true
	m := model.NewScriptedMock([]model.Response{s.streamResp}, nil)
	return m.ConsultStream(ctx, req)
}

func (s *stubClient) ListModels(ctx context.Context) ([]model.ModelInfo, error) {
	return nil, nil
}

// TestDraftClosure_FallsBackOnUnsupportedConsult: a client that signals
// ErrConsultUnsupported for blocking Consult must be drained via
// ConsultStream, and its streamed content becomes the summary.
func TestDraftClosure_FallsBackOnUnsupportedConsult(t *testing.T) {
	stub := &stubClient{
		consultErr: model.ErrConsultUnsupported,
		streamResp: model.Response{Content: "  streamed gist  ", FinishReason: "stop"},
	}
	c := NewHTTPCurator(stub, "test-model")
	draft, err := c.DraftClosure(context.Background(), memops.Thread{Body: "b"})
	if err != nil {
		t.Fatalf("DraftClosure: %v", err)
	}
	if !stub.streamCalled {
		t.Error("expected ConsultStream fallback to be reached on ErrConsultUnsupported")
	}
	if draft.Summary != "streamed gist" {
		t.Errorf("Summary = %q, want %q", draft.Summary, "streamed gist")
	}
}

// TestDraftClosure_PropagatesTransientConsultError: a non-unsupported
// Consult error (e.g. a transient network failure) must propagate
// unchanged and must NOT trigger a cost-doubling stream retry.
func TestDraftClosure_PropagatesTransientConsultError(t *testing.T) {
	transient := errors.New("http 503: service unavailable")
	stub := &stubClient{consultErr: transient}
	c := NewHTTPCurator(stub, "test-model")
	_, err := c.DraftClosure(context.Background(), memops.Thread{Body: "b"})
	if err == nil {
		t.Fatal("expected the transient Consult error to propagate, got nil")
	}
	if !errors.Is(err, transient) {
		t.Errorf("error should wrap the transient Consult error; got %v", err)
	}
	if stub.streamCalled {
		t.Error("ConsultStream must NOT be called on a transient (non-unsupported) Consult error")
	}
}

// hsym is a terse HistorySymbol constructor for the table tests.
func hsym(norm string, count, firstSeen int) memops.HistorySymbol {
	return memops.HistorySymbol{Normalized: norm, Raw: norm, Count: count, FirstSeenTurn: firstSeen}
}

// TestSelectAnchors covers the deterministic anchor selection: rank by
// count descending, tie-break by first-seen ascending, the
// AnchorProjectionMax ceiling, and the empty-history fallback to the
// thread's existing anchors. Anchor-lifecycle Inc 1 deletes the 4-floor:
// a thin (but non-empty) history yields fewer than the ceiling rather
// than triggering the fallback — the fallback fires only when there is
// no history to project from at all.
func TestSelectAnchors(t *testing.T) {
	tests := []struct {
		name string
		fm   memops.ThreadMeta
		want []string
	}{
		{
			name: "ranked by count descending",
			fm: memops.ThreadMeta{
				HistorySymbols: []memops.HistorySymbol{
					hsym("low", 1, 1), hsym("high", 9, 1),
					hsym("mid", 5, 1), hsym("top", 12, 1),
				},
				Anchors: []string{"fallback"},
			},
			want: []string{"top", "high", "mid", "low"},
		},
		{
			name: "tie on count broken by first-seen ascending (older first)",
			fm: memops.ThreadMeta{
				HistorySymbols: []memops.HistorySymbol{
					hsym("a", 3, 10), hsym("b", 3, 2),
					hsym("c", 3, 7), hsym("d", 3, 1),
				},
				Anchors: []string{"fallback"},
			},
			want: []string{"d", "b", "c", "a"},
		},
		{
			name: "capped at eight",
			fm: memops.ThreadMeta{
				HistorySymbols: []memops.HistorySymbol{
					hsym("s1", 10, 1), hsym("s2", 9, 1), hsym("s3", 8, 1),
					hsym("s4", 7, 1), hsym("s5", 6, 1), hsym("s6", 5, 1),
					hsym("s7", 4, 1), hsym("s8", 3, 1), hsym("s9", 2, 1),
					hsym("s10", 1, 1),
				},
			},
			want: []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8"},
		},
		{
			name: "thin history yields its symbols (no floor, no fallback)",
			fm: memops.ThreadMeta{
				HistorySymbols: []memops.HistorySymbol{hsym("only", 5, 1)},
				Anchors:        []string{"alpha", "beta", "gamma", "delta"},
			},
			want: []string{"only"},
		},
		{
			name: "empty normalized symbols skipped; surviving symbol wins (no fallback)",
			fm: memops.ThreadMeta{
				HistorySymbols: []memops.HistorySymbol{
					hsym("", 9, 1), hsym("real", 3, 1),
				},
				Anchors: []string{"a", "b", "c", "d", "e"},
			},
			want: []string{"real"},
		},
		{
			name: "empty history falls back to existing anchors",
			fm: memops.ThreadMeta{
				HistorySymbols: nil,
				Anchors:        []string{"alpha", "beta", "gamma", "delta"},
			},
			want: []string{"alpha", "beta", "gamma", "delta"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectAnchors(tt.fm)
			if !slices.Equal(got, tt.want) {
				t.Errorf("SelectAnchors() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDraftClosure_SummaryFromModel proves the curator uses the model's
// text as the trimmed summary and selects anchors deterministically.
func TestDraftClosure_SummaryFromModel(t *testing.T) {
	mock := model.NewScriptedMock([]model.Response{
		{Content: "  worked on the recall index and landed the embedding layer  "},
	}, nil)
	c := NewHTTPCurator(mock, "test-model")

	thr := memops.Thread{
		Meta: memops.ThreadMeta{
			ID:      "thr_7",
			Anchors: []string{"anchor-a", "anchor-b", "anchor-c", "anchor-d"},
			HistorySymbols: []memops.HistorySymbol{
				hsym("recall", 8, 1), hsym("embedding", 5, 2),
				hsym("index", 3, 3), hsym("jaccard", 2, 4),
			},
		},
		Body: "# thr_7\n\nturn excerpt body",
	}

	draft, err := c.DraftClosure(context.Background(), thr)
	if err != nil {
		t.Fatalf("DraftClosure: %v", err)
	}
	if want := "worked on the recall index and landed the embedding layer"; draft.Summary != want {
		t.Errorf("Summary = %q, want %q", draft.Summary, want)
	}
	want := []string{"recall", "embedding", "index", "jaccard"}
	if !slices.Equal(draft.Anchors, want) {
		t.Errorf("Anchors = %v, want %v", draft.Anchors, want)
	}
}
