### 2.6 Directive file format (`directives/*.md`)

Directive files are markdown documents with YAML frontmatter. The `parameters` field is reserved: no §2.6.1 key is read from it ([§2.6.1](2.6.1-recognized-parameters.md)).

```markdown
---
scope: user                         # one of: defaults | user | project
project: null                       # null for defaults/user; the project's id (prj_<n>) for project-scoped
parameters: {}                      # reserved; not read (§2.6.1)
last_modified: 2026-05-08T19:42:00-07:00
modification_source: accrual        # one of: install | user-edit | accrual (accrual = feedback directive tuning, §1.8: cross-project by default)
---

# User directives

## Revisit guidelines

[Free-form prose explaining the user-facing behavior the parameters encode. Inspectable, editable.]
```

A change proposed to the defaults or user-wide directive file, by feedback directive tuning or any
other proposer, is a behavior-memory change and follows [§1.8](../../1-operating-model/1.8-memory-vetting.md); a user's direct edit is not
prevented. By default ([§1.8](../../1-operating-model/1.8-memory-vetting.md)), feedback directive tuning proposes such a change only from patterns
aggregated across projects, and a
pattern observed in one project does not reach those files. Process memory is also directive text; a change
proposed to it follows [§1.8](../../1-operating-model/1.8-memory-vetting.md).

**Directive precedence** (highest wins):
1. Project-scoped (`directives/prj_<n>/project.md`)
2. User-wide (`directives/user.md`)
3. System defaults (`directives/defaults.md`)

The precedence list orders directive text only.

Contents:

- [§2.6.1 Recognized parameters](2.6.1-recognized-parameters.md)
