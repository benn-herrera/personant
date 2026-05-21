package memops

// Event-source names for Delta.Source (spec §3.0.1 event vocabulary).
//
// These constants are the canonical spellings of the dotted event names
// that flow through the §3.0 context-modification chain. The on-the-wire
// values must remain byte-identical to what SPEC.md §3.0 and §2.8
// document — these constants exist to keep call sites and switch arms
// in sync, not to alias the vocabulary.
const (
	SourceUserPrompt        = "user.prompt"
	SourceModelResponse     = "model.response"
	SourceFSRead            = "fs.read"
	SourceFSWrite           = "fs.write"
	SourceFSCommit          = "fs.commit"
	SourceToolResult        = "tool.result"
	SourceUserShellCapture  = "user.shell-capture"
	SourceThreadFetched     = "thread.fetched"
	SourceDigestRefresh     = "digest.refresh"
	SourceSlashInjected     = "slash.injected"
	SourceDirectiveReloaded = "directive.reloaded"
)
