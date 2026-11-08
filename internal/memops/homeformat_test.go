package memops

import (
	"errors"
	"strings"
	"testing"

	"personant/internal/version"
)

func TestGateHomeFormat(t *testing.T) {
	tests := []struct {
		name       string
		found      bool
		onDisk     int
		current    int
		allowNewer bool
		want       HomeFormatDecision
		wantErr    error
	}{
		{
			name:    "unstamped home adopts forward",
			found:   false,
			current: 1,
			want:    HomeFormatDecision{Format: version.UnversionedHomeFormat, StampFormat: version.UnversionedHomeFormat},
		},
		{
			name:       "unstamped home adopts forward under allow-newer too",
			found:      false,
			current:    1,
			allowNewer: true,
			want:       HomeFormatDecision{Format: version.UnversionedHomeFormat, StampFormat: version.UnversionedHomeFormat},
		},
		{
			name:    "matching format proceeds without a stamp",
			found:   true,
			onDisk:  1,
			current: 1,
			want:    HomeFormatDecision{Format: 1},
		},
		{
			name:    "newer home is refused",
			found:   true,
			onDisk:  2,
			current: 1,
			wantErr: ErrHomeFormatNewer,
		},
		{
			name:       "newer home passes under allow-newer, flagged",
			found:      true,
			onDisk:     2,
			current:    1,
			allowNewer: true,
			want:       HomeFormatDecision{Format: 2, NewerAccepted: true},
		},
		{
			name:    "older home migrates forward",
			found:   true,
			onDisk:  1,
			current: 2,
			want:    HomeFormatDecision{Format: 2, StampFormat: 2, MigratedFrom: 1},
		},
		{
			name:       "older home migrates forward under allow-newer",
			found:      true,
			onDisk:     1,
			current:    2,
			allowNewer: true,
			want:       HomeFormatDecision{Format: 2, StampFormat: 2, MigratedFrom: 1},
		},
		{
			name:    "unstamped home migrates forward",
			found:   false,
			current: 2,
			want:    HomeFormatDecision{Format: 2, StampFormat: 2, MigratedFrom: 1},
		},
		{
			name:    "format 2 home migrates to 3",
			found:   true,
			onDisk:  2,
			current: 3,
			want:    HomeFormatDecision{Format: 3, StampFormat: 3, MigratedFrom: 2},
		},
		{
			name:    "format 3 home migrates to 4",
			found:   true,
			onDisk:  3,
			current: 4,
			want:    HomeFormatDecision{Format: 4, StampFormat: 4, MigratedFrom: 3},
		},
		{
			name:    "older home with no registered step is refused",
			found:   true,
			onDisk:  4,
			current: 5,
			wantErr: ErrHomeFormatNoMigration,
		},
		{
			name:    "chain breaks partway",
			found:   true,
			onDisk:  1,
			current: 5,
			wantErr: ErrHomeFormatNoMigration,
		},
		{
			name:    "zero on-disk format is nonsense",
			found:   true,
			onDisk:  0,
			current: 1,
			wantErr: ErrHomeFormatInvalid,
		},
		{
			name:    "negative on-disk format is nonsense",
			found:   true,
			onDisk:  -3,
			current: 1,
			wantErr: ErrHomeFormatInvalid,
		},
		{
			name:    "nonsense current format is a caller bug",
			found:   true,
			onDisk:  1,
			current: 0,
			wantErr: ErrHomeFormatInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GateHomeFormat(tc.found, tc.onDisk, tc.current, tc.allowNewer)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("GateHomeFormat error = %v, want %v", err, tc.wantErr)
				}
				if got != (HomeFormatDecision{}) {
					t.Errorf("refused gate returned a non-zero decision: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("GateHomeFormat: unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("GateHomeFormat = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestHomeFormatMigrationChainReachesCurrent: a format bump that registers
// no step fails here, not on a user's home.
func TestHomeFormatMigrationChainReachesCurrent(t *testing.T) {
	pos := version.UnversionedHomeFormat
	prevFrom := 0
	for _, m := range homeFormatMigrations {
		if m.From <= prevFrom {
			t.Errorf("step %+v is not strictly ascending by From", m)
		}
		if m.To <= m.From {
			t.Errorf("step %+v does not advance the format", m)
		}
		prevFrom = m.From
	}
	for pos < version.CurrentHomeFormat {
		next, ok := nextHomeFormat(pos)
		if !ok {
			t.Fatalf("no step from format %d toward %d", pos, version.CurrentHomeFormat)
		}
		pos = next
	}
	if pos != version.CurrentHomeFormat {
		t.Errorf("chain ends at format %d, want %d", pos, version.CurrentHomeFormat)
	}
}

// TestGateHomeFormatNewerErrorNamesBothNumbers: the refusal has to be
// actionable — a user seeing it must learn which revision their home is
// and which one their binary speaks.
func TestGateHomeFormatNewerErrorNamesBothNumbers(t *testing.T) {
	_, err := GateHomeFormat(true, 7, 3, false)
	if err == nil {
		t.Fatal("expected refusal")
	}
	msg := err.Error()
	for _, want := range []string{"7", "3", "newer personant"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message %q does not mention %q", msg, want)
		}
	}
}
