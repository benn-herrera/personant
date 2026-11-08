#### Recall skips content already in the request (in-context exclusion)

**In-context exclusion:** an accepted intra-thread candidate is not offered again while it is in
the request; it can be offered again once its thread leaves the active set. This is not a
completeness exception — the content is in the working window. The exclusion is scoped to the
composed request: content already in it is not added a second time, whatever session or service
run the request belongs to.

