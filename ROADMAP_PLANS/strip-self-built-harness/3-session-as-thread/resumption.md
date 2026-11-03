### Resumption

- **A returned-to session keeps its thread.** The plugin passes the session ID, the service maps
  it to the thread and loads it as owner. A thread that decayed and retired meanwhile goes back to
  `active` through the existing owner resurrection rule (SPEC §3.2), and its next close re-drafts
  the summary.
- **A new session on an old topic gets a new thread and relies on recall.** At turn close the old
  thread surfaces. At or above the auto bar it is fetched into Layer B (WP7), and `derived_from`
  records the lineage through the existing §2.7.3 rule (recall-surfaced ∩ Layer B), which can then
  fire end to end. Below the bar it is logged only, and nothing about it reaches the model.

