### Model-family as platform

**Open:** U10 — opencode lets the user run any model. Either one family is tuned for Personant's
memory prompts, with a warning on others, or every model opencode runs is supported and the tag and
curator quality are measured per family (which grows the eval surface; a recall tool in place of
the topic tag, U3, shrinks it). Until decided, the Gemma family, served locally via the `reaper`
provider, is the tuning target for the memory prompts.

Adding a model family is per-family engineering, not configuration: LLM families share no
deterministic substrate to abstract over, and cross-family mismatches fail silently and
statistically, accumulating misclassified threads before recall measurably drops. Each supported
family carries (1) base intent prompts, (2) family-specific prompts, (3) a per-family eval suite.

Contents:

- [Architectural mechanism](architectural-mechanism.md)
- [Two context figures](two-context-figures.md)
