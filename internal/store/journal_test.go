package store

import (
	"os"
	"testing"
	"time"

	"personant/internal/clock"
)

func TestJournalAppendScanTruncate(t *testing.T) {
	paths := PathsForHome(t.TempDir())

	fixed := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	restore := clock.SetTimeline(func() time.Time { return fixed })
	defer restore()

	if err := AppendJournal(paths, "t_1", JournalPrompt, []byte("hello prompt")); err != nil {
		t.Fatalf("append prompt: %v", err)
	}
	if err := AppendJournal(paths, "t_1", JournalResponse, []byte("hi\nthere response")); err != nil {
		t.Fatalf("append response: %v", err)
	}

	recs, torn, err := ScanJournal(paths)
	if err != nil {
		t.Fatalf("ScanJournal: %v", err)
	}
	if torn != 0 {
		t.Errorf("torn=%d on a clean journal, want 0", torn)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Kind != JournalPrompt || string(recs[0].Bytes) != "hello prompt" {
		t.Errorf("record 0 mismatch: %+v", recs[0])
	}
	// Content bytes carrying an embedded newline must survive base64 round-trip.
	if recs[1].Kind != JournalResponse || string(recs[1].Bytes) != "hi\nthere response" {
		t.Errorf("record 1 mismatch: %+v", recs[1])
	}
	if !recs[0].At.Equal(fixed) {
		t.Errorf("At not sourced from clock.Timeline: got %v want %v", recs[0].At, fixed)
	}

	if err := TruncateJournal(paths); err != nil {
		t.Fatalf("TruncateJournal: %v", err)
	}
	recs, torn, err = ScanJournal(paths)
	if err != nil {
		t.Fatalf("ScanJournal after truncate: %v", err)
	}
	if len(recs) != 0 || torn != 0 {
		t.Fatalf("after truncate: %d records, torn=%d, want 0/0", len(recs), torn)
	}
}

func TestJournalAbsentAndTruncateAbsent(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	recs, torn, err := ScanJournal(paths)
	if err != nil || recs != nil || torn != 0 {
		t.Fatalf("ScanJournal on absent file = (%v, %d, %v), want (nil, 0, nil)", recs, torn, err)
	}
	// Truncating an absent journal is a no-op, not an error.
	if err := TruncateJournal(paths); err != nil {
		t.Fatalf("TruncateJournal on absent file: %v", err)
	}
}

// TestJournalTornTailSkipped hand-appends an un-terminated JSON fragment
// after two complete records — the on-disk signature of an append killed
// mid-write — and asserts Scan recovers the two complete records and
// counts the torn tail.
func TestJournalTornTailSkipped(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := AppendJournal(paths, "t_1", JournalPrompt, []byte("one")); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := AppendJournal(paths, "t_2", JournalPrompt, []byte("two")); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	// Simulate a torn append: a partial JSON line with no trailing newline.
	f, err := os.OpenFile(paths.TurnJournal, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for tearing: %v", err)
	}
	if _, err := f.WriteString(`{"turn":"t_3","kind":"prompt","at":"2026-07-17T10`); err != nil {
		t.Fatalf("write torn fragment: %v", err)
	}
	f.Close()

	recs, torn, err := ScanJournal(paths)
	if err != nil {
		t.Fatalf("ScanJournal: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (torn tail must be skipped, not lost-with-good-records)", len(recs))
	}
	if torn == 0 {
		t.Error("torn count = 0, want ≥1 for a skipped torn tail")
	}
	if recs[0].Turn != "t_1" || recs[1].Turn != "t_2" {
		t.Errorf("recovered wrong records: %+v", recs)
	}
}

// TestJournalInteriorCorruptLineSkipped: a garbage line that is fully
// newline-terminated (not a tail tear, but corruption) is tolerated and
// counted, and does not abort the scan of surrounding good records.
func TestJournalInteriorCorruptLineSkipped(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := AppendJournal(paths, "t_1", JournalPrompt, []byte("one")); err != nil {
		t.Fatalf("append: %v", err)
	}
	// A complete-but-unparseable line, then a valid one after it.
	f, err := os.OpenFile(paths.TurnJournal, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("this is not json\n"); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	f.Close()
	if err := AppendJournal(paths, "t_2", JournalPrompt, []byte("two")); err != nil {
		t.Fatalf("append after garbage: %v", err)
	}

	recs, torn, err := ScanJournal(paths)
	if err != nil {
		t.Fatalf("ScanJournal: %v", err)
	}
	if len(recs) != 2 || torn != 1 {
		t.Fatalf("got %d records, torn=%d; want 2 records and torn=1", len(recs), torn)
	}
}

// TestJournalAppendDurableObservable is the structural (not syscall-literal)
// fsync check: after AppendJournal returns, the content must be
// independently readable and newline-terminated on disk — the observable
// contract the per-append fsync exists to guarantee.
func TestJournalAppendDurableObservable(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := AppendJournal(paths, "t_1", JournalResponse, []byte("durable")); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(paths.TurnJournal)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("journal not newline-terminated on disk: %q", raw)
	}
}

func TestJournalRejectsBadInput(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := AppendJournal(paths, "", JournalPrompt, []byte("x")); err == nil {
		t.Error("empty turnID accepted")
	}
	if err := AppendJournal(paths, "t_1", JournalKind("bogus"), []byte("x")); err == nil {
		t.Error("unknown kind accepted")
	}
}

// TestJournalOwner covers the in-flight-turn signal reader (the
// R3-addendum marker-into-journal fold): absent/empty = no turn, first
// record = owner, torn or corrupt first line = in flight with unknown
// owner (never adoptable).
func TestJournalOwner(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		paths := PathsForHome(t.TempDir())
		owner, inFlight, err := JournalOwner(paths)
		if err != nil || inFlight || owner != "" {
			t.Fatalf("absent journal: (%q,%v,%v), want (\"\",false,nil)", owner, inFlight, err)
		}
	})
	t.Run("empty-after-truncate", func(t *testing.T) {
		paths := PathsForHome(t.TempDir())
		if err := AppendJournal(paths, "t_1", JournalPrompt, []byte("p")); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := TruncateJournal(paths); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		owner, inFlight, err := JournalOwner(paths)
		if err != nil || inFlight || owner != "" {
			t.Fatalf("empty journal: (%q,%v,%v), want (\"\",false,nil)", owner, inFlight, err)
		}
	})
	t.Run("first-record-owns", func(t *testing.T) {
		paths := PathsForHome(t.TempDir())
		if err := AppendJournal(paths, "t_7", JournalPrompt, []byte("p")); err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := AppendJournal(paths, "t_7", JournalResponse, []byte("r")); err != nil {
			t.Fatalf("append: %v", err)
		}
		owner, inFlight, err := JournalOwner(paths)
		if err != nil || !inFlight || owner != "t_7" {
			t.Fatalf("owned journal: (%q,%v,%v), want (t_7,true,nil)", owner, inFlight, err)
		}
	})
	t.Run("torn-first-line-unknown-owner", func(t *testing.T) {
		paths := PathsForHome(t.TempDir())
		if err := os.WriteFile(paths.TurnJournal, []byte(`{"turn":"t_9","kind":"pro`), 0o644); err != nil {
			t.Fatalf("seed torn journal: %v", err)
		}
		owner, inFlight, err := JournalOwner(paths)
		if err != nil || !inFlight || owner != "" {
			t.Fatalf("torn journal: (%q,%v,%v), want (\"\",true,nil)", owner, inFlight, err)
		}
	})
	t.Run("corrupt-first-line-unknown-owner", func(t *testing.T) {
		paths := PathsForHome(t.TempDir())
		if err := os.WriteFile(paths.TurnJournal, []byte("{not json}\n"), 0o644); err != nil {
			t.Fatalf("seed corrupt journal: %v", err)
		}
		owner, inFlight, err := JournalOwner(paths)
		if err != nil || !inFlight || owner != "" {
			t.Fatalf("corrupt journal: (%q,%v,%v), want (\"\",true,nil)", owner, inFlight, err)
		}
	})
}
