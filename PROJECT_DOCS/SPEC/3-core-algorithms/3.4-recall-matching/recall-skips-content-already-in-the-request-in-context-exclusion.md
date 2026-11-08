#### Recall skips content already in the request (in-context exclusion)

**In-context exclusion:** a fetched intra-thread candidate is not fetched again while it is in
the request; it can be fetched again once its thread leaves the active set. This is not a
completeness exception — the content is in the composed request. The exclusion is scoped to the
composed request: content already in it is not added a second time, whatever session or service
run the request belongs to.

