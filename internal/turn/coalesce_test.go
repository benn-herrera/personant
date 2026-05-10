package turn

import (
	"sort"
	"testing"
)

func TestCoalesceUnion(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("alpha")
	b.addSymbol("alpha") // dup
	b.addSymbol("beta")
	b.addThread("thr_42")
	b.addThread("thr_42")
	b.addThread("thr_99")

	got := b.symbolList()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("symbols: got %v want [alpha beta]", got)
	}

	gotT := b.threadList()
	sort.Strings(gotT)
	if len(gotT) != 2 || gotT[0] != "thr_42" || gotT[1] != "thr_99" {
		t.Errorf("threads: got %v want [thr_42 thr_99]", gotT)
	}
}

func TestCoalesceReset(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("x")
	b.addThread("thr_1")
	b.reset()
	if len(b.symbols) != 0 || len(b.threads) != 0 {
		t.Errorf("reset did not clear: %+v", b)
	}
}

func TestCoalesceIgnoresEmpty(t *testing.T) {
	b := newCoalesceBuffer()
	b.addSymbol("")
	b.addThread("")
	if len(b.symbols) != 0 || len(b.threads) != 0 {
		t.Errorf("empty entries should not be added: %+v", b)
	}
}
