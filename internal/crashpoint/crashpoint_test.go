package crashpoint

import (
	"errors"
	"testing"
)

func TestUnarmedIsNoOp(t *testing.T) {
	// A never-armed point must not panic and must report unarmed.
	At("test.neverArmed")
	if Armed("test.neverArmed") {
		t.Fatal("Armed reported true for a point that was never armed")
	}
}

func TestArmTriggersCrashSentinel(t *testing.T) {
	const name = "test.armTrigger"
	disarm := Arm(name)
	defer disarm()

	if !Armed(name) {
		t.Fatal("Armed reported false immediately after Arm")
	}

	var crash *Crash
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("At did not panic while armed")
			}
			err, ok := r.(error)
			if !ok || !errors.As(err, &crash) {
				t.Fatalf("panic value %v (%T) is not a *Crash", r, r)
			}
		}()
		At(name)
	}()

	if crash.Point != name {
		t.Fatalf("Crash.Point = %q, want %q", crash.Point, name)
	}
}

func TestDisarmRestoresNoOp(t *testing.T) {
	const name = "test.disarm"
	disarm := Arm(name)
	disarm()

	if Armed(name) {
		t.Fatal("Armed reported true after disarm")
	}
	// Must no longer panic.
	At(name)
}

func TestDisarmClosureIsIdempotent(t *testing.T) {
	const name = "test.doubleDisarm"
	disarm := Arm(name)
	disarm()
	disarm() // second call must not blow up
	Disarm("never.armed.at.all")
}

func TestArmingOneDoesNotArmAnother(t *testing.T) {
	const armedName = "test.isolated.armed"
	const otherName = "test.isolated.other"
	disarm := Arm(armedName)
	defer disarm()

	if Armed(otherName) {
		t.Fatal("arming one point armed an unrelated point")
	}
	At(otherName) // must be a no-op even though anyArmed is globally true
}

func TestRegisterEnumeratesSortedAndDeduped(t *testing.T) {
	Register("zzz.later")
	Register("aaa.earlier")
	Register("aaa.earlier") // duplicate: idempotent

	names := RegisteredNames()

	var seenA, seenZ int
	for _, n := range names {
		switch n {
		case "aaa.earlier":
			seenA++
		case "zzz.later":
			seenZ++
		}
	}
	if seenA != 1 {
		t.Errorf("duplicate registration not deduped: aaa.earlier appears %d times", seenA)
	}
	if seenZ != 1 {
		t.Errorf("zzz.later appears %d times, want 1", seenZ)
	}

	// Sorted enumeration for the coverage gate.
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("RegisteredNames not sorted: %q before %q", names[i-1], names[i])
		}
	}
}

// mustCrash runs fn and asserts it panics with a *Crash at point.
func mustCrash(t *testing.T, point string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected crash at %s, got none", point)
		}
		c, ok := r.(*Crash)
		if !ok || c.Point != point {
			t.Fatalf("panic value %v (%T), want *Crash at %s", r, r, point)
		}
	}()
	fn()
}

func TestArmOnHitFiresOnNthHit(t *testing.T) {
	const name = "test.armOnHit"
	disarm := Arm(name, ArmOnHit(3))
	defer disarm()

	// Hits 1 and 2 pass through; while pending, Armed reports false (the
	// NEXT At will not fire) until the countdown reaches 1.
	if Armed(name) {
		t.Fatal("Armed true with 3 hits remaining")
	}
	At(name)
	At(name)
	if !Armed(name) {
		t.Fatal("Armed false with the next hit due to fire")
	}
	mustCrash(t, name, func() { At(name) })

	// The point stays armed at the fire threshold: a re-execution after
	// the simulated crash still crashes (tests defer disarm to end it).
	mustCrash(t, name, func() { At(name) })
}

func TestArmOnHitClampsToOne(t *testing.T) {
	const name = "test.armOnHitClamp"
	disarm := Arm(name, ArmOnHit(0))
	defer disarm()
	if !Armed(name) {
		t.Fatal("ArmOnHit(0) did not clamp to fire-next")
	}
	mustCrash(t, name, func() { At(name) })
}
