## What it does

Personant is a memory system under opencode. This plan removes everything in the tree that served a
harness of Personant's own, and makes the memory work underneath run without a user in the loop:

- **The self-built harness goes:** the chat REPL, terminal, shell escape, tool surface and the
  `chat`, `ping` and `models` verbs, with their tests, recipes and dependencies. Nothing replaces
  them and nothing waits on the plugin route (ROADMAP B1).
- **Personant has no inference path or provider configuration of its own.** The provider pool,
  `providers.toml`, the home's `config.toml`, API-key handling and the `[user]`, `[chat]` and
  `[search]` settings go. The curator, dissector and vetting reviewer will reach models through
  opencode's `experimental.generate`, and embeddings through opencode (ROADMAP A12a); building
  those channels is A5's and A12a's work, not this plan's.
- **`version.FrontEnd` is retired** with its bump contract, `test-fe` and `fe-scope-check`. The
  substrate semver line and the home format stay.
- **Flow control goes; the memory work stays autonomic.** Personant drops acks and ack modes, the
  closure review queue, recall and closure offers, decline categorization, startup project prompts,
  the recovery banner, memory-native commands and the REPL's presentation hooks. Threads still
  decay and retire, recall still fetches, and project identity still resolves, all with no
  resolver installed. Recall candidates below the auto threshold are logged only; nothing about
  them reaches the model.
- **Personant never reads the user's repository.** Its only git is its own memory stores. A
  project is opencode's project directory and a bundle of threads; its identity comes from that
  directory alone, so a moved directory is a new project. Remote-URL identity, local→remote
  promotion, remote collision, file-move detection and git-root discovery go, with `meta.json`'s
  `remote_urls`, `historical_remote_urls` and `historical_root_paths`.
- **Interrupted turns keep Personant's rule:** a retracted prompt never enters memory, whatever
  opencode's transcript shows. Personant composes its own context.
- **Clean break for configuration:** no migration code and no stale-file warning. The one stored
  format change, the `meta.json` field removal, takes home format 4 with a restamp-only
  migration (WP9). The substrate line takes one PATCH bump (WP10).

Out of scope: the plugin and service (A3, A5), the opencode generation and embedding channels
(A5, A12a), `~/.config/personant/config.toml` (A12, A12a: its only setting names an opencode
provider whose embedding route is unestablished, so a loader now would have no reader), and the
session-as-thread redesign, which section 3 assigns to a follow-on plan.

