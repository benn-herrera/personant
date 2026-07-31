package tools

import "context"

// Turn identity on the tool context.
//
// A handler that budgets itself PER TURN needs to know when the turn
// changed, and it has no other way to find out: the registry is built
// once at startup, a handler is a plain function, and internal/tools is
// deliberately unaware of internal/turn. Passing the turn number through
// the context the turn loop already owns is the whole mechanism — an int,
// not a turn object, so this package stays turn-unaware.
//
// It is deliberately NOT a reset callback the turn loop must remember to
// call. A monotonic counter compared against a stored one cannot be
// forgotten at one call site and silently leak budget across turns.

type turnKey struct{}

// ContextWithTurn returns ctx carrying the current turn number. The turn
// loop calls it once per round, before dispatching.
func ContextWithTurn(ctx context.Context, turn int) context.Context {
	return context.WithValue(ctx, turnKey{}, turn)
}

// TurnFromContext returns the turn number carried by ctx, or 0 when the
// context carries none (a direct Dispatch from a test, or a caller that
// has no turn). Zero is a usable key: it groups every turn-less call
// together, which is the conservative direction for a per-turn budget.
func TurnFromContext(ctx context.Context) int {
	turn, _ := ctx.Value(turnKey{}).(int)
	return turn
}
