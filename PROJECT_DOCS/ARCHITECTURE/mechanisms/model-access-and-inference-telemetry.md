### Model access and inference telemetry

Standard pattern: THESIS "The building blocks", row "Model access".

opencode makes the provider call, so the memory service sees no provider response and receives none
of the extended inference-timing block that `model.inference-telemetry` logs (`ttft_ms`,
`prefill_ms`, `gen_ms`, `total_ms`, tokens/second and token counts; see "Event log format"). Token
counts reach the memory service through opencode's step and usage events (`session.step.ended`); no
source for the timing fields is established.

**Open:** the source of the inference-timing block under opencode. Only the in-process turn emits `model.inference-telemetry` today (`internal/turn/turn.go`), from its own provider response.

The local provider is **oMLX**, an OpenAI-compatible endpoint, configured in opencode's own
provider settings.

Production design, not built: The memory service's judgments go through the host (SPEC §8.2.1); text generation for the
summarizer, clusterer and recall judgment through the plugin's `experimental.generate` (an opencode plugin hook, integration-options report §2.3) over
opencode's providers; the embedder names an opencode provider in `[embedding]` (SPEC §8.2.2); how
embeddings are requested through opencode is Open A12a (SPEC §8.2.1). Where `[embedding] vectorLength` is set,
the embeddings request carries it as the `dimensions` parameter.

