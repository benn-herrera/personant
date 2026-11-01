#### Architectural mechanism

Keep the model-family surface narrow and isolated: the memory prompts
(`internal/prompt`, `internal/curator`), output-stream interpretation and embedding model
assumptions. Everything else (substrate, port boundaries, recall scoring, dedup, autonomic git) is
model-agnostic and survives substrate migration cleanly.

