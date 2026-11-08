### Archive index (`archive/index.jsonl`)

Standard pattern: THESIS "The building blocks", row "Cold storage".

[Authoritative] One JSON line per archived thread, sorted by `thr_id`; SPEC §3.8.1 states what it
must hold. The completed entry:

```jsonl
{"thr_id":"thr_42","commit_hash":"<H>","parent_commit_hash":"<capture commit; H's parent>","tree_hash":"<tree sha of threads/thr_42/ at the capture commit>","archived_at":"<RFC3339>","original_path":"threads/thr_42/","spine_summary":"<summary at archival time>","anchors":["..."],"project":"prj_3","recovered_at":""}
```

`spine_summary` and `anchors` are copied verbatim from the thread registry record at archival time.
`parent_commit_hash` is the capture commit whose tree still holds the thread bytes; a restore reads
from it directly rather than walking `commit_hash`'s first parent, which picks the wrong lineage on
a merge commit. An entry whose `commit_hash` is still empty is an **archive entry awaiting
back-fill**. `recovered_at` is empty until the thread is restored. Losing the index loses no data:
the thread bytes stay reachable from the capture commit's tree.

---

