package memops

// Log-event categories for MemoryOps.Log (spec §2.8 event-log format).
//
// Categories are a finite, controlled vocabulary; actions trail off into
// a much longer tail and stay as free-form strings at the call site.
// On-the-wire spellings must remain byte-identical to what SPEC.md §2.8
// documents.
const (
	LogCategoryArchive     = "archive"
	LogCategoryConsolidate = "consolidate"
	LogCategoryDedup       = "dedup"
	LogCategoryFS          = "fs"
	LogCategoryModel       = "model"
	LogCategoryProject     = "project"
	LogCategoryRecall      = "recall"
	LogCategoryRecovery    = "recovery"
	LogCategoryRetire      = "retire"
	LogCategorySession     = "session"
	LogCategorySpine       = "spine"
	LogCategoryStaging     = "staging"
	LogCategorySystem      = "system"
	LogCategoryThread      = "thread"
	LogCategoryTopic       = "topic"
	LogCategoryWorkset     = "workset"
)
