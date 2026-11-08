#### Superseded symbols still match in recall (lifecycle-aware symbolic scorer)

Symbolic recall matches a thread on its anchors and on every symbol retained in its history, so a
superseded symbol stays matchable. A matched symbol counts 1 if it is active in that thread and
`recall.superseded-weight` (default 1.0, no down-weight) if superseded; a symbol that is also an
anchor counts as active; the union the score divides by is unchanged. The default is lowered below
1.0 only if the simulation shows abandoned threads crowding out focused ones ([§9](../../9-measurement-and-validation-regime/index.md)).

