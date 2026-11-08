## Applying the thesis

THESIS lists the standard pattern each piece follows. This implementation applies two design rules at every layer:

> **Deterministic state as authoritative. The model in narrow judgment roles.**

| Component | Role |
|---|---|
| Deterministic (Go service) | authoritative state holder; integrity enforcer; build/query/index operations; automatic git management of the store directory |
| Model | narrow generative/judgment roles: topic tagging, summary drafting (the summarizer, SPEC §2.2), recognition, clustering for fallback dissection (the clusterer, SPEC §3.6), memory-vetting review |
| Host (opencode) | the terminal UI, tools, permissions, providers and every interaction mode. Personant defines no interaction mode or workflow (SPEC §1). |

---

