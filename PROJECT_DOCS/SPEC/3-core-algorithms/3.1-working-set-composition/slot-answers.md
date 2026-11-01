#### Slot answers

Every slot — the five layers and the three above — defines four things before it
ships: (1) its **budget**; (2) its **internal priority**, what is dropped first when its content
exceeds its share; (3) its **overflow form**, what stands in for dropped content — the honest
truncation marker, or an entry that names the content and can reproduce it ([§1.4](../../1-operating-model/1.4-evicted-content.md)); (4) its
**underusage**, whether it lends or borrows unused share.

**Underusage flows from fixed slots to dynamic ones; a lender reclaims first.** A fixed slot —
directives, project conventions and instructions, the A1 spine — changes only on a visible event at
a turn boundary, so its unused share is idle and is lent. A dynamic slot — live turn, B, C, and the
opencode-expectations slot, whose todo and background-job updates arrive mid-turn — varies turn to
turn and does not lend. When a lender grows it takes
its share back at the next composition, and the borrower gives way through the existing eviction
order (C-oldest, then B-oldest). Fixed slots are composed first, so the request keeps a stable
prefix across turns.

**Open:** per-slot answers — all four for the instructions and tool-definition slots (including
whether the tool-definition slot lends); budget, internal priority and overflow form for the
opencode-expectations slot. Candidate overflow forms for tool definitions: omit rarely used tools from a request, or list them
by name with definitions on demand (MCP tools are recognizable by the `<server>_` name prefix).
Without a tool-definition slot the whole-request ceiling above cannot be enforced.

