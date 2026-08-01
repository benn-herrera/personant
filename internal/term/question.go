package term

import (
	"errors"
	"io"
)

var (
	// ErrAborted is returned by [Terminal.ReadLine] when the user
	// interrupted at the prompt. It replaces chat's errInputAborted; the
	// REPL loop treats it as a session interrupt, the interactive
	// resolvers treat it like io.EOF (decline / defer).
	ErrAborted = errors.New("term: input aborted")

	// ErrQuestion reports a caller error at the read boundary, not a user
	// one, so it is returned rather than absorbed. Two things produce it:
	// a malformed [Question] (see [Question.Keys] for the one combination
	// that is rejected) and an [Activity] that cannot read a line (see
	// [Terminal.ReadLine] for which two can).
	//
	// [IsEndOrAbort] deliberately does not match it: a resolver that
	// treats "stop asking" and "I called this wrong" alike would swallow
	// its own bug as a user decline.
	ErrQuestion = errors.New("term: malformed question")
)

// IsEndOrAbort reports whether err ends interactive input: end of stream
// (Ctrl-D) or an abort (Ctrl-C). Both mean "stop asking", and every
// resolver needs the same test, so it is named once here.
func IsEndOrAbort(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, ErrAborted)
}

// Question is everything [Terminal.ReadLine] needs to ask AND read in
// one call. Folding the question into the read is the fix for bug 5;
// there is no other read path, so the write-then-read shape that caused
// it cannot be spelled.
type Question struct {
	// Preamble is multi-line question text — a menu's options, a list of
	// recall candidates, an explanation. Each element is one line, and
	// term COMMITS them through the output channel before the editor
	// starts. They are never handed to the editor as a prompt.
	//
	// That split is mandatory, not tidy (constraint R2-14). liner
	// derives its prompt width with countMultiLineGlyphs, which does not
	// treat an embedded "\n" as a row break, so a multi-line preamble
	// passed as a prompt corrupts its erase math — W2 would ship a new
	// bug while fixing bug 5. The property survives liner's removal: a
	// committed line is scrollback and an editor must not repaint over
	// scrollback, whatever is doing the editing.
	//
	// It also makes the off-TTY bytes today's bytes minus the empty
	// prompt, so the sim and scenario goldens are unchanged by
	// construction.
	Preamble []string

	// Prompt is the single-line question the editor renders and must
	// not repaint above — "choice: ", "> ", "id or name: ".
	Prompt string

	// Default is a pre-filled, editable value: the line starts
	// containing it with the cursor at the end, and the user can accept,
	// edit, or clear it. This is today's PromptWithSuggestion — the
	// §3.5 summary edit and the retracted-turn re-offer.
	//
	// Off a TTY there is nothing to pre-fill, so the plain backend
	// prints it as a hint and returns it unchanged for an empty line,
	// preserving today's semantics exactly.
	Default string

	// Keys is the set of single keystrokes that answer this question
	// without Enter — "ynq", "rdawes". Empty means a full edited line,
	// which is the normal case.
	//
	// It is a FAST PATH, not a mode. If the FIRST keystroke is in the
	// set, the read returns immediately with that key. If it is not, the
	// keystroke becomes the first character of a normally edited line,
	// so a menu offering "[a]ll / [n]one" still accepts "1 3 5". Both
	// halves are required: the menus in chat.go genuinely have both
	// shapes, and a design that forces one loses answers.
	//
	// Matching is exact on the rune as typed. Case folding is the
	// caller's, because the caller is the one that knows whether "Y" and
	// "y" mean the same thing.
	//
	// Keys and Default are MUTUALLY EXCLUSIVE: a value the user is meant
	// to edit cannot also be dismissed by its first keystroke. Setting
	// both returns [ErrQuestion] rather than silently picking one.
	Keys string
}

// Answer is what the user gave.
type Answer struct {
	// Text is the EFFECTIVE answer as text, always. On the single-key
	// path it is string(Key), so a caller that only cares about the value
	// can switch on Text and never look at Key — which is what lets the
	// seven existing menu sites keep their shape through the migration.
	//
	// "Effective" is load-bearing where [Question.Default] is set:
	// accepting the default with a bare Enter returns the DEFAULT'S TEXT,
	// not "". Both backends agree, by different routes — on a TTY the
	// line was pre-filled and is submitted unedited, and off a TTY an
	// empty line returns the default unchanged (today's
	// PromptWithSuggestion semantics). A caller wanting "" must clear the
	// line, which is a thing the user can do and the caller can see.
	Text string

	// Key is the keystroke that resolved a single-key answer, or 0 when
	// the user typed and submitted a line. The distinction it carries is
	// PATH, not content: single-key fast path versus edited line. The key
	// 'n' is Text=="n" with Key=='n'; any submitted line — including a
	// bare Enter, with or without a Default behind it — is Key==0.
	//
	// It exists for the caller that must tell a keystroke from a typed
	// character of the same value, which [Question.Keys] makes possible
	// by design.
	Key rune
}
