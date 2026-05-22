package store

import "personant/internal/memops"

// SymbolRecord is one line of symbols.jsonl (spec §2.4). Derived inverse
// index: symbol → threads where it appears.
type SymbolRecord struct {
	Symbol         string              `json:"symbol"`
	Threads        []string            `json:"threads"`
	AnchorIn       []string            `json:"anchor_in"`
	SupersededIn   []string            `json:"superseded_in"`
	SourceDominant memops.SymbolSource `json:"source_dominant"`
}
