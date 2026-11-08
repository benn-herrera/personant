#### Slot policies

Every slot — the five memory slots, the live-turn reserve and the three above — defines four things before it
ships: (1) its **budget**; (2) its **internal priority**, what is dropped first when its content
exceeds its share; (3) its **overflow form**, what stands in for dropped content — the honest
truncation notice, or a stand-in line that names the content and can reproduce it ([§1.4](../../1-operating-model/1.4-evicted-content.md)); (4) its
**unused-share rule**, whether it gives up unused share to other slots or takes unused share from
them.

**Unused-share reallocation** (a slot's unused share made available to other slots) **flows from
fixed slots to dynamic ones; the slot giving up share reclaims it first.** A fixed slot —
directives (project convention files included), instructions, the registry — changes only on a visible event at
a turn boundary, so its unused share is idle and is given up. A dynamic slot — live turn,
active-threads, dormant-threads, and the opencode-expectations slot, whose todo and background-job
updates arrive mid-turn — varies turn to turn and does not give up share. When a slot that gave up
share grows it takes its share back at the next composition, and the slot that received it gives
way through the existing demotion order (oldest dormant thread first, then oldest active thread).
Fixed slots are composed first, so the request keeps a stable prefix across turns.

**Open:** per-slot policies — all four for the instructions and tool-definition slots (including
whether the tool-definition slot gives up unused share); budget, internal priority and overflow form for the
opencode-expectations slot. Candidate overflow forms for tool definitions: omit rarely used tools from a request, or list them
by name with definitions on demand.
Without a tool-definition slot the whole-request ceiling above cannot be enforced.

