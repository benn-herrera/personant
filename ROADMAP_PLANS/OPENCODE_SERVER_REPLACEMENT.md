# OPENCODE SERVER REPLACEMENT – Personant

**Status:** fallback. The active route integrates through opencode's server plugin hooks with opencode unmodified
([ACTIVE_DESIGN.md](../ACTIVE_DESIGN.md)). This plan applies if that route cannot give the memory system the control it
needs.

## BLUF
We're going to convert this project from re-inventing what an AI harness is and narrow it to solving the agent memory problem.
See [THESIS.md](../THESIS.md). We're going to adopt [opencode](https://github.com/anomalyco/opencode)'s TUI and implement
a compliant back end server to run under it. For the agent management and network protocol systems we'll borrow opencode's existing
design whole and re-implement in Go. Our only major development contribution will be the long-lived memory system.

## Theme: Solve only one hard problem
The memory system is our real work. Everything else is going to use the great solutions built by other people.
Borrow designs, make reasonable use of high-quality imports. Wheel re-inventing is explicitly off the table.

**Conform to opencode; do not evaluate it.** Outside context management, where opencode has made a choice we match it,
whether or not we would have chosen differently. Nobody examines or second-guesses those choices: the question is always
"what does opencode do", never "should we do it differently".

**Boundary.** Ours: what enters the model's context — compaction, summarization, context assembly, and the sub-mind
split and re-merge. opencode's: everything else, including session storage, the message/part data model, the event
stream, agents, permissions, tools and config. Session handling contains both sides; where the seam falls inside it is
traced in the ported code, not decided in advance.

Rewriting all of our contract docs is on the table here. There are portions that leaned too heavily into 'human looks at every detail'
which made sense to an extent, but were probably diluting human attention too much. Under the new expected workflows it
was definitely too far. A new balance will need to be found.

## Sequence: works first, then change it
1. A Go server that the released opencode TUI runs on as if it were opencode's own server, with behavior matching
   opencode's — compaction included. Parity is checked by differential testing: the same requests against opencode's
   server and ours, comparing responses and event streams.
2. Only then: trace where context management cuts into the session path, and change it there.

Target release: opencode v2.2.2. The TUI attaches to an external server with `opencode --server <url>`
(see [OPENCODE_TUI_LAUNCH.md](../OPENCODE_TUI_LAUNCH.md)). The contract reference is
[OPENCODE_SERVER_SURVEY.md](../OPENCODE_SERVER_SURVEY.md); the authoritative route definition is opencode's generated
`packages/protocol/openapi.json`, with event payload shapes taken from its generated client types.

### Port priority
Route groups per survey §1.4.
0. **Boot** — the TUI comes up with populated lists: `info`, `fs/list`, `location`, `event`, `config`, `agent`,
   `model`, `provider`, `command`, `skill`, `project`. Requires config, agent, command and skill loading (survey §3).
1. **One working conversation** — session create/list/get/prompt, message list/get, interrupt, streaming events,
   permission list/reply, form list/reply.
2. **Agent management and workflow** — agent/model switching, `command`, `fork`, subagent dispatch, inbox,
   background, wait, compaction.
3. **Workspace** — vcs, diff, fs read/find, revert, shell, worktree, pty.
4. **Connections** — integrations, credentials, OAuth, MCP.
5. **Deferred** — remaining experimental routes.

Code Mode (the `execute` tool) is denied at first, an opencode-supported configuration that exposes its tools
directly. What supporting it would take: [code-mode-support.md](code-mode-support.md).
Undecided: opencode's JavaScript plugins, which a Go server cannot run as-is.

## Rework Outline

### Leverage opencode
opencode has a really nice design split where the TUI is separate from core behaviors and they communicate via loopback HTTP.
A compliant implementation of their back end spec can sit under their polished TUI and work equivalently.
A clone of the opencode repo exists as a sibling to this one.

### Agent control features
Examine opencode's implementation and re-implement equivalent in Go. Their design appears to be really good, so let's use it.

### AI endpoint protocol comms and session management
Adopt opencode's design and port it. Our existing implementation is replaced, not compared.

### Config, agent, command, skill loading
This lives in their back end. Examine and re-implement. Tighter compliance adds some testing but saves a ton
of pain down the road.

opencode's config files are read exactly as opencode reads them. Personant-specific settings live in a separate file
under `~/.config/personant/` and contain no setting that overlaps or conflicts with opencode's.

### Main session memory and agent memory
Here's where things get interesting. The main session memory we have a solid design intent for. We also have a design for
sub-mind split off with optional re-merge to the main mind. The original workflows envisioned were more R&D assistant and less
general coding agent. This is where we'll be doing the bulk of our work going forward.
