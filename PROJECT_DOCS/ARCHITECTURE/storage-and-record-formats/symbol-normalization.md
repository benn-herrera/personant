### Symbol normalization

Categories: `identifier` (pattern-extractable: file paths, URLs, code symbols, claim ids, commit
SHAs), `entity` (model-emitted named concepts), `tag` (user-emitted `#hash`).

| Category | Case | Whitespace | Punctuation | Notes |
|---|---|---|---|---|
| identifier | preserved | preserved | preserved | exact match required |
| entity | lowercase | hyphenated | stripped except `-`, `.`, `_` | stop words dropped from multi-word forms |
| tag | lowercase | hyphenated | stripped except `-` | leading `#` stripped |

Multi-word entity stop words (per-project additions in `meta.json`): the, a, an, of, in, on, at,
by, for, to, with, from, as, is, are, was, were, be, been, being. They are dropped only from
multi-word entities; a single-token entity that is a stop word is preserved. Storage is
`{raw, normalized}`: matching uses `normalized`, display uses `raw`.

