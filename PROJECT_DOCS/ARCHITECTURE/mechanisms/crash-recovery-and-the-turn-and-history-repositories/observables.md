#### Observables

Recovery reads the following on-disk signals and classifies them into exactly one case, numbered
1–12 under "Recovery cases". No signal is trusted transitively; each is read against the correct
repository.

- **Journal as turn signal.** A non-empty `turn-journal.jsonl` is the in-flight-turn signal; there
  is no separate marker file. Its first record carries the turn id (`store.JournalOwner`), and the
  first append's fsync makes the signal durable. An `op=turn` marker file left by an older binary
  is still read and reconciled.
- **Batch marker.** `op-in-progress.json` (store directory, gitignored, survives `reset --hard`)
  covers batch operations only: `{op: archival|sleep|recovery|barrier|rebaseline, day?, orig?}` (`rebaseline`: the mid-day turn-repository re-baseline, see "Mechanism names"),
  where `sleep` is consolidation and `barrier` is the day rollover. It is written before a batch
  operation's authoritative mutations and cleared as the last step. Its presence is the
  batch-in-flight signal, and `op=barrier` carries the target `day`.
- **Watermark.** `derived-watermark` (gitignored) holds the turn repository HEAD hash the derived
  files were last built from. Turn repository HEAD equal to the watermark, with the store working
  tree clean against that HEAD, means derived files are fresh: the O(1) clean-open certificate.
- **Trailers.** Every turn-repository commit carries `Personant-Turn: <id>`, read as the head turn
  id; every history-repository commit carries `Personant-Day: <N>`, read as the head day number.
  The head turn id discriminates the torn-turn cases (4, 5, 6); the head day number drives
  new-day detection and the day-commit guard. The head day number is undefined only for a bare,
  never-committed history repository.
- **Turn repository state** — present, missing or half-created — is evaluated defensively and
  first, before any turn-repository read. A failure to open a corrupt or partial `.git-daily`
  classifies as half-created and is never a propagated error, so a corrupt turn repository is
  recreated from the store working tree and never wedges an open.

