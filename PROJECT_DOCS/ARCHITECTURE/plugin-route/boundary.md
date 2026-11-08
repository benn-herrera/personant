### Boundary

Outside context management, Personant uses what opencode has chosen, as it is; the one exception is cleaning up the rows its checkpoint compactions leave in opencode's database (SPEC §1.7; see "opencode database cleanup"). Where
opencode's machinery would decide what the model sees (compaction, history replay), the plugin
takes that decision over through a hook; it does not reimplement the machinery. The host is
opencode, not Charm's Crush: Crush reads provider API keys from environment variables and runs
its TUI in-process with its backend, so there is no protocol to sit under.
