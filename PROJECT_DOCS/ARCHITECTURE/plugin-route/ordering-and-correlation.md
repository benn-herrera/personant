### Ordering and correlation

- **Tool results apply in tool-call order.** opencode runs one response's tool calls concurrently
  (probe results §6, lesson 3), so arrival order is not repeatable. The service holds tool results
  and applies them in call order at the next `session.context` call or at turn close. This makes
  memory state independent of completion order (SPEC §1.5). The prototype reads each call's
  position from the id its mock executor assigns (`call_<step>_<index>`); **Open:** the production source of tool-call
  order — a real provider's call ids carry no such index, so the order must come from the response
  itself, and no source is established.
- **Sequence numbers are taken in the plugin as each event arrives,** with forwarding chained,
  because RPCs can reach the service out of order (probe results §6, lesson 6).
- **Events correlate by opencode message ID,** not by "the current turn", so a resumed execution's
  late events cannot close another turn (resume probe §5.3, item 3).
- **Model requests are counted at the provider side,** never from `session.model.request`, which
  reports `title` and `compaction` kinds even when hooks answered them (probe results §6, lesson
  4).
- **File content.** `fs.write` content comes from the `write` tool's input; its output is only a
  confirmation line (probe results §6, lesson 5). The tracked-file store holds file bytes
  (SPEC §3.9.1), while opencode's `read` returns formatted output (a header and `N: ` line
  prefixes); the prototype stores that output as returned (probe results §9, item 3). **Open:** how
  the production service recovers the file bytes from `read`'s formatted output.

