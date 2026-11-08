## Project identity

| Level | Form | Mutable? | Purpose |
|---|---|---|---|
| Internal handle | `prj_<n>` | no — assigned at creation | storage key (`projects/prj_<n>/`); thread registry reference |
| Display name | the directory's base name at creation | no | rendering in composed requests and digests |

**Project resolution** when an opencode session starts (SPEC §4.5.7) reads only opencode's project
directory, never anything inside it. `store.ResolveActiveProject` returns the project whose
`current_root_path` is the cleaned directory (`StepPathMatch`), else creates a project rooted there
and named `filepath.Base` of it (`StepCreated`; `FileAdapter.ResolveActiveProject` logs
`project.created`), else, with no directory, `prj_default` (`StepDefault`). A directory not seen
before is a new project, moved or not; nothing detects or repairs a move.

---
