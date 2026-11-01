### 2.1 Storage layout

Memory lives under one home directory (`$PERSONANT_HOME`, default `~/.personant`). Canonical
memory is plain text, versioned in git, and inspectable with ordinary tools. A user's direct edit
to canonical memory is never destroyed by recovery: it is absorbed ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md) cell 3). Files derived
from canonical memory are regenerable and never hand-edited; a state-changing git operation on the
home tree fails while any derived file is stale. The file tree, the canonical/derived/operational
classification and the storage commands: ARCHITECTURE.md "Storage and record formats"; the
commands are listed in [§4.1](../../4-user-surface/4.1-cli-command-surface.md).

Contents:

- [§2.1 Secret-bearing content](secret-bearing-content.md)
- [§2.1 Outside `$PERSONANT_HOME`](outside-personant-home.md)
