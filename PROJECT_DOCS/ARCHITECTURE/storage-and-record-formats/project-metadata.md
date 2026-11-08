### Project metadata

**`projects/prj_<n>/meta.json`** (authoritative). Three identity levels: the internal handle `id`
(`prj_<n>`, never changes), the remote identity `remote_urls[0]` (normalized git remote, gained
when the tree first acquires one) and the display name `name`.

```typescript
interface ProjectMeta {
  id: string;                        // /^prj_\d+$/, or the reserved prj_default
  name: string;                      // /^[a-z][a-z0-9-]*$/; mutable
  current_root_path: string;         // absolute
  historical_root_paths?: string[];  // appended on a root move; never rewritten
  remote_urls?: string[];            // normalized; primary = remote_urls[0]
  historical_remote_urls?: string[]; // de-duplication memory
  created: string;                   // RFC3339
  last_active: string;               // RFC3339; updated on any engagement
  thread_count: number;              // not maintained: nothing writes it, so it reads 0; a project's thread count is digest.json's
  conventions_paths: string[];       // absolute paths to convention files loaded into the directives slot
  symbol_patterns: ProjectPattern[]; // project-specific deterministic-pass patterns
  ignore_symbols: string[];          // project-scoped stopword additions
}
interface ProjectPattern { name: string; regex: string /* RE2 */; category: SymbolCategory; }
```

Remote URL normalization: lowercase host; strip trailing `.git`; `git@host:owner/name` and
`https://host/owner/name` are the same URL. `prj_default` is reserved for threads created before any
project was active: empty path fields, `name: default`.

**`projects/prj_<n>/digest.json`** ([derived], regenerated with the other derived files: at the day rollover, archival, restore and `personant index rebuild`; other-projects slot content):

```typescript
interface ProjectDigest {
  project: string;            // prj_<n>
  display_name: string;       // resolved at render time
  thread_count: number;
  recent_anchors: string[];   // top N anchors across the N most recently engaged threads
  one_line_summary: string;   // ≤ 80 chars: recent_anchors comma-joined and truncated
  byte_size: number;          // should be ≤ cross-project.digest-per-project-bytes
}
```

