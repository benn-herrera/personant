### The model drafts, deterministic code writes

The LLM never directly mutates canonical state. It proposes content; deterministic code validates
it and writes it.

- **Closure:** the curator drafts the retirement summary; deterministic code writes the thread file
  and spine entry (SPEC §3.5).
- **Recall:** a deterministic match scores candidates; the fetch into Layer B is an explicit
  deterministic operation (SPEC §3.4).
- **Behavior memory:** every proposed change goes through one reviewed write path (see "Memory
  vetting").

A confirmation the user clears reflexively verifies nothing, so a routine closure and a recall
candidate above its auto threshold apply without asking. Each is recorded `ack=auto`, which keeps
the accept-rate and edit-rate measures counting only outcomes a user actually answered.

