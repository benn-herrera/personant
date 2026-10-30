# ACTIVE PLAN – Personant

## BLUF
We're going to convert this project from re-inventing what an AI harness is and narrow it to solving the agent memory problem.
See [THESIS.md](THESIS.md). We're going to adopt [opencode]([ht](https://github.com/anomalyco/opencode)'s TUI and implement
a compliant back end server to run under it. For the agent management and network protocol systems we'll borrow opencode's existing
design whole and re-implement in Go. Our only major development contribution will be the long-lived memory system.

## Theme: Solve only one hard problem
The memory system is our real work. Everything else is going to use the great solutions built by other people.
Borrow designs, make reasonable use of high-quality imports. Wheel re-inventing is explicitly off the table.

Rewriting all of our contract docs is on the table here. There are portions that leaned too heavily into 'human looks at every detail'
which made sense to an extent, but were probably diluting human attention too much. Under the new expected workflows it
was definitely too far. A new balance will need to be found.

## Rework Outline

### Leverage opencode
opencode has a really nice design split where the TUI is separate from core behaviors and they communicate via loopback HTTP.
A compliant implementation of their back end spec can sit under their polished TUI and work equivalently.
A clone of the opencode repo exists as a sibling to this one.

### Agent control features
Examine opencode's implementation and re-implement equivalent in Go. Their design appears to be really good, so let's use it.

### AI endpoint protocol comms and session management
We have a basic system implemented. Look at opencode's and see what makes sense to add to our feature list. If ours is actually
hopelessly naive compared to theirs, then we adopt their design. No pride on this point.

### Config, agent, command, skill loading
This probably lives in their back end. Again - examine and re-implement. Tighter compliance adds some testing but saves a ton
of pain down the road.

### Main session memory and agent memory
Here's where things get interesting. The main session memory we have a solid design intent for. We also have a design for
sub-mind split off with optional re-merge to the main mind. The original workflows envisioned were more R&D assistant and less
general coding agent. This is where we'll be doing the bulk of our work going forward.
