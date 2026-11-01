### Invariants

1. **Byte-exact round trip.** Unbinding what was bound gives back the same names and byte-identical
   content, including empty files, files with no trailing newline, and content that looks like
   JSON, YAML delimiters (`---`), `## Turn` headings, CRLF, U+2028/U+2029 or NUL.
2. **Excerpt content is valid UTF-8.** It arrives through JSON decoding, which never yields invalid
   UTF-8. Bind asserts `utf8.Valid` on every excerpt it is about to write into a folio (Go's JSON
   encoder would silently replace invalid bytes with U+FFFD), and a violation fails the barrier
   with an error — never a skip, never a repair; no file is left loose to cope with it. A file
   whose encoded line would exceed `ReadJSONL`'s line cap stays loose
   (`barrier.folio-skip reason=too-large`).
3. **Never neither, never twice.** At every instant, including any crash point, each excerpt name
   is readable from at least one tier. Readers return each name once: when a name is in both tiers,
   they read the loose copy.
4. **Tiers change only at the day barrier.** Bind and unbind run only inside step B1b, under the
   `op=barrier` marker, and land in that barrier's B2 day-commit. The turn path still writes only
   loose files. Per-turn staging, journalling and the turn crash cells are unchanged. A warmed
   thread's excerpts move back to loose files at the next barrier, never on demand.
5. **Placement is a pure function of canonical state.** The settle step's target for each excerpt
   depends only on the spine record's state and the thread's set of excerpt names. Threads are
   processed in ascending id order and entries in ascending (number, name) order. Folio contents,
   order and barrier commits are therefore deterministic (SPEC §1.5).
6. **Settle converges from any crash state.** Rerunning settle on the same inputs finishes the
   moves. A name in both tiers with identical bytes is a move that did not finish: settle keeps the
   copy in the target tier and removes the other.
7. **A hand edit is never destroyed.** If a name is in both tiers with different bytes, nothing
   moves for that name: both copies stay, `barrier.folio-skip reason=conflict` is logged, readers
   use the loose copy and verify warns (SPEC §2.1). If a folio is malformed, settle skips the whole
   thread (`reason=unreadable`) and verify reports an error (WP3). B2's verify gate then handles it
   as it handles a hand-broken spine today (`internal/memops/fileadapter/fileadapter_barrier.go`
   :388-392: `if !recovering { return fmt.Errorf(... "day-commit spine check: %d structural
   error(s)" ...) }`): the **live** barrier fails with the `op=barrier` marker kept and nothing
   committed; the **next open's** roll-forward completion (`completePending` → `barrierTail(...,
   true)`) runs `spineQuarantineRepair`, which moves the broken folio byte-exact into the barrier
   quarantine directory and scoped-restores `threads/<id>/folio.jsonl` from primary HEAD (the last
   day-commit's folio). `spineQuarantineRepair` is path-generic: `findingRel`
   (`fileadapter_barrier.go:534-550`) strips a `[line]` suffix and relativises the path,
   `quarantineAndRemove` (`:950-967`) renames any home-relative file, and the restore is a
   path-scoped `autogit.Checkout` (`:451`). A folio absent from HEAD is an orphan add: quarantined
   and removed, its bytes kept in the quarantine directory. Because tiers change only at a barrier
   (invariant 4) and settle skipped the broken thread, HEAD's folio is consistent with the thread's
   loose files, so invariant 3 holds after the restore.
8. **Folio before loose.** Settle relies on this ordering. After settle, every folio entry sorts
   before every loose entry of the same thread, so the folio is a prefix and the loose files a
   suffix. Readers that are satisfied by the loose files skip the folio. Only a hand-made loose
   excerpt numbered below the folio's highest entry can break the ordering, and only for window
   reads until the next barrier, which re-establishes it.

