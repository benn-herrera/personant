#### Anchor projection at owner-turn close

After the owner's symbol history is merged, the memory service
re-derives the thread's anchors ([§2.7.4](../../2-data-model/2.7-symbol-normalization-and-categories/2.7.4-symbol-lifecycle-and-anchor-projection.md)) and records them in its registry record. Engaged-but-not-owner threads do not re-project. Closing a thread runs a final projection
from its symbol history.

