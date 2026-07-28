package chat

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"personant/internal/memops"
	"personant/internal/model"
	"personant/internal/turn"
)

// blockingClient is a model.Client whose stream parks in the middle of the
// response and stays there until the turn's context is cancelled — i.e.
// exactly where the user is sitting when they reach for Esc.
type blockingClient struct {
	entered chan struct{} // closed once the stream is live
}

func (c *blockingClient) Consult(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, model.ErrConsultUnsupported
}

func (c *blockingClient) ListModels(context.Context) ([]model.ModelInfo, error) {
	return []model.ModelInfo{{ID: "test-model"}}, nil
}

func (c *blockingClient) ConsultStream(ctx context.Context, _ model.Request) (model.StreamReader, error) {
	return &blockingStream{ctx: ctx, entered: c.entered}, nil
}

type blockingStream struct {
	ctx     context.Context
	entered chan struct{}
	sent    bool
}

func (s *blockingStream) Next() (model.Chunk, error) {
	if !s.sent {
		s.sent = true
		close(s.entered)
		return model.Chunk{Content: "partial "}, nil
	}
	<-s.ctx.Done()
	return model.Chunk{}, s.ctx.Err()
}

func (s *blockingStream) Final() model.Response { return model.Response{Content: "partial "} }
func (s *blockingStream) Close() error          { return nil }

// TestEscAbortLeavesNoRecoveryScope is the #94 half of the feature, and
// the reason the abort window is gated on pre-canonical phases.
//
// An Esc abort is a ROUTINE user action. If it left the turn's recovery
// scope open, the NEXT launch would greet the user with a recovery banner
// — a routine action looking like a crash. The claim under test is that a
// cancel delivered while the model is streaming lands inside the turn's
// pre-canonical window, so turn.RunWithInfo's deferred handler releases
// the scope and the following Reconcile reports Quiet.
func TestEscAbortLeavesNoRecoveryScope(t *testing.T) {
	paths := scaffoldHome(t)
	project := memops.ProjectMeta{ID: "prj_1", Name: "alpha", CurrentRootPath: paths.Home}
	writeMeta(t, paths, project)

	ctx := context.Background()
	ops := newOps(paths)
	if err := ops.Init(ctx, memops.InitOptions{Quiet: true}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := ops.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	client := &blockingClient{entered: make(chan struct{})}
	state, err := turn.LoadSession(ctx, ops, project, memops.Provider{DefaultModel: "test-model"}, client)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}

	ctl, _, _ := newTestControl(t)
	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()
	// Arm the abort core directly: the test session has no terminal, so
	// there is no cbreak mode or watcher goroutine to stand in for the key.
	ctl.abortTurn = turnCancel
	state.OnPhase = ctl.onPhase

	done := make(chan error, 1)
	go func() {
		_, rerr := turn.Run(turnCtx, state, "a prompt the user changed their mind about", io.Discard)
		done <- rerr
	}()

	select {
	case <-client.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the model stream never started")
	}
	ctl.onEsc()

	select {
	case rerr := <-done:
		if rerr == nil {
			t.Fatal("the aborted turn reported success")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the turn did not unwind after the abort")
	}
	if !ctl.tookAbort() {
		t.Fatal("the abort was not recorded")
	}

	// The load-bearing assertion: the next open is an ordinary one.
	report, err := ops.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile after abort: %v", err)
	}
	if !report.Quiet() {
		var banner bytes.Buffer
		printRecoveryBanner(&banner, report)
		t.Errorf("an aborted turn made the next open non-quiet — it would print:\n%s",
			strings.TrimRight(banner.String(), "\n"))
	}
}
