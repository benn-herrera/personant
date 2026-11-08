### 2.1 Storage layout

Memory lives under one store directory (default `~/.personant`; the environment variable that
overrides it is in [§8.2.3](../../8-bootstrap-and-lifecycle/8.2-configuration-sources-and-precedence/8.2.3-environment-variables.md)). The **memory store** is that directory with its git history.
Authoritative files are plain text, versioned in git, and inspectable with ordinary tools. A user's
direct edit to an authoritative file is never destroyed by crash recovery: it is absorbed ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)), or, where a torn turn's reset would revert it, preserved byte-exact in
quarantine (a directory of recovered bytes, [§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)) first. Files derived from authoritative files are regenerable and never hand-edited; a
day rollover or archival batch, which writes the **history repository** (the memory store's permanent day-grain git repository, [§3.11](../../3-core-algorithms/3.11-memory-store-commit-cadence/index.md)),
fails if it leaves a derived file stale ([§8.3](../../8-bootstrap-and-lifecycle/8.3-memory-store-validation-in-automatic-git-operations/index.md)); between **day rollovers** (the once-a-day
maintenance sequence, [§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md) (5)) derived files may lag by design ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md) (7)). **Operational**
files are the gitignored runtime state the memory service manages to run (the turn journal, the
last-active record, the working-set record), neither authoritative nor derived. The file
tree, the authoritative/derived/operational classification and the storage commands: ARCHITECTURE
"Storage and record formats"; the commands are listed in [§4.1](../../4-user-surface/4.1-cli-command-surface.md).

Contents:

- [§2.1 Secret-bearing content](secret-bearing-content.md)
- [§2.1 Outside the store directory](outside-the-store-directory.md)
