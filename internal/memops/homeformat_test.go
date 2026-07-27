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
			// Unreachable in production today (current == 1 is the only
			// format), defined so the first migration has a shape to fill.
			name:    "older home has no migration path",
			found:   true,
			onDisk:  1,
			current: 2,
			wantErr: ErrHomeFormatNoMigration,
		},
		{
			name:       "older home is refused even under allow-newer",
			found:      true,
			onDisk:     1,
			current:    2,
			allowNewer: true,
			wantErr:    ErrHomeFormatNoMigration,
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
