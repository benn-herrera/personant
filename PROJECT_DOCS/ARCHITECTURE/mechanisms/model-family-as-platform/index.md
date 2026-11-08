### Model-family as platform

**Open:** U10 — whether one model family is tuned for the memory prompts (the prompts that ask the model for narrow memory judgments: topic tagging, summary drafting, clustering, recognition) or every model opencode runs is supported and measured per family (ROADMAP U10). Until decided, the Gemma family, served locally via the `reaper` provider, is the tuning target for the memory prompts.

Adding a model family is per-family engineering, not configuration: model families share no
deterministic foundation to abstract over, and cross-family mismatches fail silently and
statistically, accumulating misclassified threads before recall measurably drops. Each supported
family carries (1) base intent prompts, (2) family-specific prompts, (3) a per-family evaluation
suite.

Contents:

- [Isolating the model-family surface](isolating-the-model-family-surface.md)
- [Token ceiling and model context window](token-ceiling-and-model-context-window.md)
