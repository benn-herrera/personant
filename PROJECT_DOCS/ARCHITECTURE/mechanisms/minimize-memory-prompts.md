### Minimize memory prompts

> **Every memory prompt is a tax paid forever. Every deterministic algorithm is a fixed cost paid
> once.**

The model is non-deterministic, costly, slow, opaque, and — because of the
[model-family-as-platform](model-family-as-platform/index.md) principle — every memory prompt multiplies:
each prompt requires re-tuning per supported family.

The corollary: **even within model tasks, keep prompts narrow.** Constrain the input space;
constrain the output space; validate output deterministically before trusting it. A broadened
prompt expands both the family-tuning surface and the silent-drift surface.

