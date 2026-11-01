### Serving deployment

opencode makes the provider call, so the service sees no provider response and receives none of the
extended inference-timing block that `model.inference-telemetry` logs (`ttft_ms`, `prefill_ms`,
`gen_ms`, `total_ms`, tokens/second and token counts; see "Event log format"). Token counts reach
the service through opencode's step and usage events (`session.step.ended`); no source for the
timing fields is established.

**Open:** the source of the inference-timing block under opencode; the event is retired from the
vocabulary if there is none.

The local provider is **oMLX**, an OpenAI-compatible endpoint, configured in opencode's own
provider settings.

**Open:** U5 — how the service reaches its own models (the curator, the dissector, the embedder and
recall judgment). (a) Through the plugin's `experimental.generate` over opencode's providers, which
leaves no overlap; the embedder still needs Personant configuration because opencode has no
embedding provider. (b) Through a Personant provider pool under `~/.config/personant/`, which must
not name an endpoint opencode's providers also name.

