package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// DecodeArgs is the double-encoding unwrapper every handler goes
// through, so its edge cases are the edge cases of every tool call the
// runtime will ever service.
func TestDecodeArgs(t *testing.T) {
	type argShape struct {
		URL   string `json:"url"`
		Count int    `json:"count"`
	}

	for _, tc := range []struct {
		name    string
		raw     string
		want    argShape
		wantErr bool
	}{
		{"provider shape: object inside a JSON string", `"{\"url\":\"https://x/y\",\"count\":3}"`, argShape{"https://x/y", 3}, false},
		{"bare object", `{"url":"https://x/y","count":3}`, argShape{"https://x/y", 3}, false},
		{"empty object", `{}`, argShape{}, false},
		{"empty string envelope", `""`, argShape{}, false},
		{"empty blob", ``, argShape{}, false},
		{"whitespace blob", "  \n ", argShape{}, false},
		{"padded object", "  {\"url\":\"u\"}  ", argShape{URL: "u"}, false},
		{"malformed json", `{"url":`, argShape{}, true},
		{"malformed inside the envelope", `"{\"url\":"`, argShape{}, true},
		{"wrong type for a field", `{"count":"three"}`, argShape{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got argShape
			err := DecodeArgs(json.RawMessage(tc.raw), &got)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeArgs: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}

	if err := DecodeArgs(json.RawMessage(`{}`), nil); err == nil {
		t.Error("a nil destination was accepted")
	}
}

// TestTurnContext — the per-turn budgeting seam. Absent turn identity
// must be a usable key rather than a panic or a special case.
func TestTurnContext(t *testing.T) {
	if got := TurnFromContext(context.Background()); got != 0 {
		t.Errorf("bare context turn = %d, want 0", got)
	}
	ctx := ContextWithTurn(context.Background(), 42)
	if got := TurnFromContext(ctx); got != 42 {
		t.Errorf("turn = %d, want 42", got)
	}
	if got := TurnFromContext(ContextWithTurn(ctx, 43)); got != 43 {
		t.Errorf("re-keyed turn = %d, want 43", got)
	}
}
