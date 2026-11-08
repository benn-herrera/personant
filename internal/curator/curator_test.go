package curator

import (
	"context"
	"errors"
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
	m := model.NewScriptedMock([]model.Response{s.streamResp})
	return m.ConsultStream(ctx, req)
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
	summary, err := c.DraftClosure(context.Background(), memops.Thread{Body: "b"})
	if err != nil {
		t.Fatalf("DraftClosure: %v", err)
	}
	if !stub.streamCalled {
		t.Error("expected ConsultStream fallback to be reached on ErrConsultUnsupported")
	}
	if summary != "streamed gist" {
		t.Errorf("summary = %q, want %q", summary, "streamed gist")
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

// TestDraftClosure_SummaryFromModel proves the curator uses the model's
// text as the trimmed summary.
func TestDraftClosure_SummaryFromModel(t *testing.T) {
	mock := model.NewScriptedMock([]model.Response{
		{Content: "  worked on the recall index and landed the embedding layer  "},
	})
	c := NewHTTPCurator(mock, "test-model")

	summary, err := c.DraftClosure(context.Background(), memops.Thread{Body: "# thr_7\n\nturn excerpt body"})
	if err != nil {
		t.Fatalf("DraftClosure: %v", err)
	}
	if want := "worked on the recall index and landed the embedding layer"; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
}
