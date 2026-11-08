### Symbol index (`symbols.jsonl`)

[Derived] Inverse index, sorted by `symbol`, rebuilt from `spine.jsonl` and `threads/*/thread.md`
frontmatter on every index refresh.

```typescript
interface SymbolRecord {
  symbol: string;               // normalized form
  threads: string[];            // thread ids where it appears anywhere (anchor or history)
  anchor_in: string[];          // subset where it is an anchor
  superseded_in: string[];      // subset where its history entry is superseded (SPEC §3.4)
  source_dominant: SymbolSource; // highest-ranked source across emissions (see "Symbol source")
}
```

`threads` and `superseded_in` are sorted by descending `recall_fires` (most-recalled first).
`superseded_in` is rebuilt from the frontmatter `lifecycle`, the only source of supersession state.

