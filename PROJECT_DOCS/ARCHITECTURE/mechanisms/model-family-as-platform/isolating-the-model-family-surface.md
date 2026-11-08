#### Isolating the model-family surface

Keep the model-family surface narrow and isolated: the memory prompts
(`internal/prompt`, `internal/curator`), output-stream interpretation and embedding model
assumptions. Everything else (the memory store, port boundaries, recall scoring, dedup, automatic
git) is model-agnostic and survives a move to another storage backend cleanly.

