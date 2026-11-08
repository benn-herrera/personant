#### Close ordering

The response is the turn's `session.text.ended` texts concatenated in event
order, with the last `session.step.ended` token counts as usage (the prototype's buffering, probe
results §9, item 4; `internal/opencodehook/service.go`, `close`).

**Open:** the resumed turn (SPEC §3.0.4; see "Start order and resume"). No phase covers an entry
on a context call with no preceding prompt: the prototype service answers it with an error
(`compose`). Undecided: which `BeginTurn` steps a resumed turn runs, and what its `user.prompt`
content is — opencode's synthetic continue text or the original prompt (resume probe §5.3, item 1).

**Open:** mid-turn prompt — a `steer` prompt (a prompt the user sends while a turn is running) can arrive after tool results; whether it opens a new
turn or joins the turn in progress (SPEC §3.0.4). `BeginTurn` is written for one prompt per turn.

