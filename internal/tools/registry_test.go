package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"personant/internal/model"
)

// okTool builds a trivially-valid Tool returning body.
func okTool(name, body string) Tool {
	return Tool{
		Spec:    model.ToolSpec{Name: name, Description: name + " description"},
		Handler: func(context.Context, json.RawMessage) ([]byte, error) { return []byte(body), nil },
	}
}

func TestRegisterRejects(t *testing.T) {
	cases := []struct {
		name string
		tool Tool
		want error // nil → just require some error
	}{
		{"empty name", Tool{Handler: okTool("x", "y").Handler}, nil},
		{"nil handler", Tool{Spec: model.ToolSpec{Name: "web.fetch"}}, nil},
		{
			"mutating",
			Tool{
				Spec:    model.ToolSpec{Name: "fs.propose_delete"},
				Handler: okTool("x", "y").Handler,
				Mutates: true,
			},
			ErrMutatingToolUnsupported,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := NewRegistry()
			err := r.Register(c.tool)
			if err == nil {
				t.Fatalf("Register(%+v): want error, got nil", c.tool.Spec.Name)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("Register: err = %v; want errors.Is %v", err, c.want)
			}
			if r.Len() != 0 {
				t.Errorf("rejected tool was registered anyway (Len=%d)", r.Len())
			}
		})
	}
}

// TestRegisterRejectsDuplicate — a second registration under the same name
// is an error, not a silent overwrite.
func TestRegisterRejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(okTool("web.fetch", "one")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(okTool("web.fetch", "two")); err == nil {
		t.Fatal("duplicate Register: want error, got nil")
	}
	tool, ok := r.Lookup("web.fetch")
	if !ok {
		t.Fatal("web.fetch missing after duplicate rejection")
	}
	got, err := tool.Handler(context.Background(), nil)
	if err != nil || string(got) != "one" {
		t.Errorf("duplicate overwrote the original: got %q, %v", got, err)
	}
}

// TestEmptyRegistrySpecsIsNil — the property that keeps the `tools` field
// off the wire: an empty registry (and a nil one) yields NO specs, so
// model.Request.Tools stays empty and encodeRequest omits the field. An
// empty tool list would invite calls nothing can service.
func TestEmptyRegistrySpecsIsNil(t *testing.T) {
	for _, r := range []*Registry{nil, NewRegistry()} {
		if got := r.Specs(); got != nil {
			t.Errorf("Specs() on an empty registry = %v; want nil", got)
		}
		if got := r.Len(); got != 0 {
			t.Errorf("Len() = %d; want 0", got)
		}
		if _, ok := r.Lookup("web.fetch"); ok {
			t.Error("Lookup on an empty registry reported a hit")
		}
	}
}

// TestSpecsDeterministicOrder — registration order must not reach the
// request. Map iteration order is unspecified, so an unsorted Specs would
// reshuffle the tool block between otherwise identical turns: the request
// stops being reproducible and the provider's prompt cache misses on a
// prefix that did not semantically change.
func TestSpecsDeterministicOrder(t *testing.T) {
	names := []string{"web.search", "fs.read", "model.consult", "fs.grep", "web.fetch"}
	want := []string{"fs.grep", "fs.read", "model.consult", "web.fetch", "web.search"}

	// Two registries built in opposite orders must produce the same specs.
	forward, reverse := NewRegistry(), NewRegistry()
	for i, n := range names {
		if err := forward.Register(okTool(n, "x")); err != nil {
			t.Fatalf("forward Register %s: %v", n, err)
		}
		rn := names[len(names)-1-i]
		if err := reverse.Register(okTool(rn, "x")); err != nil {
			t.Fatalf("reverse Register %s: %v", rn, err)
		}
	}
	for _, r := range []*Registry{forward, reverse} {
		specs := r.Specs()
		if len(specs) != len(want) {
			t.Fatalf("Specs len = %d; want %d", len(specs), len(want))
		}
		for i, s := range specs {
			if s.Name != want[i] {
				t.Errorf("Specs[%d] = %q; want %q", i, s.Name, want[i])
			}
		}
	}
	// And repeated calls on one registry agree — the sort is not sampling
	// a stable-by-luck map order.
	for i := 0; i < 8; i++ {
		specs := forward.Specs()
		for j, s := range specs {
			if s.Name != want[j] {
				t.Fatalf("Specs() call %d position %d = %q; want %q", i, j, s.Name, want[j])
			}
		}
	}
}

// TestSpecsCarryParameters — the JSON-Schema fragment passes through
// verbatim; the registry does not validate or rewrite it.
func TestSpecsCarryParameters(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`)
	r := NewRegistry()
	tool := okTool("web.fetch", "body")
	tool.Spec.Parameters = schema
	if err := r.Register(tool); err != nil {
		t.Fatalf("Register: %v", err)
	}
	specs := r.Specs()
	if len(specs) != 1 {
		t.Fatalf("Specs len = %d; want 1", len(specs))
	}
	if string(specs[0].Parameters) != string(schema) {
		t.Errorf("Parameters = %s; want %s", specs[0].Parameters, schema)
	}
	if specs[0].Description != "web.fetch description" {
		t.Errorf("Description = %q", specs[0].Description)
	}
}
