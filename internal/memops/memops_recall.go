package memops

// EmbeddingDebtCap is the §6.5 debt window: the count of turn-excerpts a
// thread may accrue scrolled-out-of-the-assembly-window before the turn
// loop enqueues a §3.4 fine-tier flush of that thread (and resets its
// debt). At an assembly window of 512 a cap of 16 means the fine tier lags
// the assembly window by at most ~3%, and a continuously active thread
// flushes ~32 times per window's worth of scroll-out — frequent enough to
// keep recent early content recallable within a bounded lag, infrequent
// enough that the embed-call rate stays well under one batch per turn. A
// §9 calibration window, not a frozen value. The substrate sizes the
// excerpts it keeps cheaply readable from it, so a change here moves that
// boundary too.
//
// PUBLIC CONTRACT. This value is exported because it is the boundary of the
// flush-lag dead zone the acceptance sim's B1 completeness-floor band
// legitimately consumes (internal/scenarios/sim references it directly to
// mark which probe targets only the #123 lexical floor can surface). Per
// ARCHITECTURE.md's harness↔system coupling rule, a contract value the
// harness depends on is exported and referenced — never hand-mirrored into a
// second const that can silently drift.
const EmbeddingDebtCap = 16
