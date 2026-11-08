#### Recall skips content already in the request (in-context exclusion)

**In-context exclusion:** the exclusion is scoped to the composed request: content already in it
is not added a second time, whatever session or service run the request belongs to. An
intra-thread match is recorded as a match-fire and not fetched: nothing yet delivers a thread's
earlier excerpts into the composed request (not built, ROADMAP "Intra-thread recall finds but does
not deliver").

