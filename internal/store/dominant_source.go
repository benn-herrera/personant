package store

// DominantSource implements the spec §2.7.3 source-precedence rule:
//
//	curator > user > model > deterministic
//
// When a normalized symbol is observed from two sources — within a single
// turn (per-turn coalescing) or across turns (cumulative history merge) —
// the dominant source wins. An empty source is treated as the lowest
// rank, so any non-empty source beats unset.
//
// This is a pure precedence helper with no I/O; it lives in store rather
// than turn or index so both packages (and any future consumer that
// merges symbol provenance) can call it without crossing the
// turn → index dependency direction.
func DominantSource(a, b SymbolSource) SymbolSource {
	if sourceRank(a) >= sourceRank(b) {
		return a
	}
	return b
}

// sourceRank returns the §2.7.3 precedence rank. Higher rank wins.
// An empty/unrecognized source ranks 0, which lets DominantSource
// upgrade an unset value to any concrete source on first sighting.
func sourceRank(s SymbolSource) int {
	switch s {
	case SourceCurator:
		return 4
	case SourceUser:
		return 3
	case SourceModel:
		return 2
	case SourceDeterministic:
		return 1
	}
	return 0
}
