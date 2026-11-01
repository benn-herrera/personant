### No speculative prefetch

Every loaded thread is loaded because it was recognized as needed, never guessed.

**Open:** U3 — how a fetch is triggered under opencode: keep the in-band topic tag with a stream
abort-and-re-issue, or replace it with a recall tool. The in-band tag needs a spike (read the head
of the streaming response in the `session.http.response` hook, cancel upstream, re-issue, return
the second stream), per-protocol wire code in the plugin, and a way to strip the tag from the
stored transcript and the user-visible text, for which the service has no route. A recall tool is
a registered fetch tool whose result enters context at the next step, at the cost of one extra
model step per fetch, and rewrites SPEC §5.1/§5.5 and the tag-fidelity measures. The mid-turn
fetch does not fire under opencode today (PR §5). Until decided, the mechanism below binds:

Spine is always in window → the model recognizes a prior topic from spine entries → emits a topic
tag → the corresponding threads are fetched into Layer B → the response is generated. The model is
the recognition oracle, not a heuristic guess.

