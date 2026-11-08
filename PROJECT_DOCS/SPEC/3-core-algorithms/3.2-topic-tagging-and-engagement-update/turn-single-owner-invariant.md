#### Turn-single-owner invariant

A **turn belongs to exactly one thread** — its *owner*. A turn may
*engage* several threads; only the owner receives the turn's excerpt. The memory service assigns ownership; the topic tag
([§5.1.1](../../5-model-interaction/5.1-topic-tag-emission-format/5.1.1-format.md)) is advisory input.

