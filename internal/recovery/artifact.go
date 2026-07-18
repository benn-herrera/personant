package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"personant/internal/memops"
	"personant/internal/store"
)

// Preserve+surface of journaled turn content (SOLUTION principle 7).
// The rolled-back turn's bytes are written to a human-readable artifact
// under recovery/ and announced via the report and event log — NEVER
// replayed into canonical. A turn is bytes plus non-replayable
// derivation (symbol extraction, acks, owner selection); replaying the
// bytes alone would manufacture exactly the internally-inconsistent
// state recovery exists to eliminate. The user re-supplies the content
// through the normal channel if wanted.

// preserveJournalContent writes the artifact for the scanned journal
// records (no-op when there are none) and stamps the report. The
// artifact name embeds a content hash, so a re-entered recovery pass
// rewrites the same file byte-identically (idempotent) and distinct
// crashes can never clobber each other's evidence.
func preserveJournalContent(paths store.PersonantPaths, rep *memops.RecoveryReport, turnID string, records []store.JournalRecord) error {
	if len(records) == 0 {
		return nil
	}
	if turnID == "" {
		turnID = records[0].Turn
	}
	content := renderArtifact(turnID, records)
	sum := sha256.Sum256([]byte(content))
	name := fmt.Sprintf("recovered-turn-%s-%s.md", sanitizeName(turnID), hex.EncodeToString(sum[:4]))
	dst := filepath.Join(paths.RecoveryDir, name)

	if err := os.MkdirAll(paths.RecoveryDir, 0o755); err != nil {
		return fmt.Errorf("recovery: mkdir artifact dir: %w", err)
	}
	if err := store.WriteFileAtomic(dst, []byte(content)); err != nil {
		return fmt.Errorf("recovery: write artifact: %w", err)
	}
	rep.PreservedContentPath = dst
	rep.PreservedTurn = turnID
	logEvent(paths, "journal-recovered", fmt.Sprintf("turn=%s records=%d path=%s", turnID, len(records), name))
	return nil
}

// renderArtifact formats the journal records as markdown. Fully
// deterministic for a given record set — timestamps come from the
// records themselves, never the wall clock — so a re-entered recovery
// pass renders byte-identical content and lands on the same
// hash-suffixed artifact name.
func renderArtifact(turnID string, records []store.JournalRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# personant — recovered turn %s\n\n", turnID)
	b.WriteString("Content journaled before an unclean shutdown. It was rolled back from\n")
	b.WriteString("canonical memory and is preserved here only — it has NOT been replayed.\n")
	b.WriteString("Re-supply it through a normal turn if still wanted.\n")
	for _, r := range records {
		fmt.Fprintf(&b, "\n## %s — turn %s (%s)\n\n", r.Kind, r.Turn, r.At.UTC().Format(time.RFC3339))
		b.Write(r.Bytes)
		if len(r.Bytes) == 0 || r.Bytes[len(r.Bytes)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// sanitizeName keeps a turn id filesystem-safe. Turn ids are internal
// tokens, but this artifact path crosses a boundary (report → banner →
// user), so the check runs regardless.
func sanitizeName(s string) string {
	if s == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}
