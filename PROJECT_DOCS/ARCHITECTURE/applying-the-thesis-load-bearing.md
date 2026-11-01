## Applying the thesis (load-bearing)

THESIS.md states why Personant exists. This implementation applies two design rules at every layer:

> **Deterministic state as canonical. LLM in narrow judgment roles.**

When making a design decision, ask:

1. Could deterministic code do this without involving the LLM?
2. If the LLM is involved, is its role narrow and well-bounded?

| Tier | Role |
|---|---|
| Deterministic (Go service) | canonical state holder; integrity enforcer; build/query/index operations; autonomic git management of `~/.personant/` |
| LLM | narrow generative/judgment roles: topic tagging, summary drafting, recognition, dissection clustering, memory-vetting review |
| Harness (opencode) | the terminal UI, tools, permissions, providers and every interaction mode. Personant defines no interaction mode or workflow (SPEC §1). |

---

