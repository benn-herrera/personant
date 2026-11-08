#### Who supplies the reason

Under the default `recall.ack-mode: banded` the user is NOT asked to
categorize a decline. A banded decline logs `not-relevant`, except that a candidate already resident
in the active set is declined `already-known` by the memory service itself. Under `recall.ack-mode: always` the
reason is asked. **Open:** U4 — the channel that carries the question.

§4.3.1 (REPL line editing and history) and §4.3.2 (turn progress indicator) are unused: opencode
renders the terminal UI. §4.3.4 (tool receipts) is unused: opencode shows tool calls.

