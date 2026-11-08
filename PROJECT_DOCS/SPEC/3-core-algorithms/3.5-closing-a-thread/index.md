### 3.5 Closing a thread

Trigger detection (engagement decay or an explicit close request, `/done`) → summarizer-drafted
summary → outcome → frontmatter + thread registry update → demotion out of the active and dormant
sets. Cold storage ([§3.8](../3.8-cold-storage/index.md)) follows a close under thread registry size pressure (the registry
holding more records than archival allows, [§3.8](../3.8-cold-storage/index.md)).

**Open:** U4 — the channel for every interactive step below (the exception drain, `/done`, the
`[e]dit` choice, `always` mode, the pending-queue announcement) and the command that triggers each.
The rules for decay, classification, the derived queue and the summarizer draft are decided.

Engagement decay fires when a thread has gone unengaged for at least `engagement.decay-turns` turns OR at least
`engagement.decay-time` since its `last_engaged` ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)): `decay-time` catches extended user absence, `decay-turns` catches
low-frequency engagement in a busy session.

The summarizer-drafted summary targets 100–150 chars: summary + anchors must fit the registry
slot's budget at realistic thread registry size while letting the model recognize prior engagement
without fetching the body.

**Routine closings are auto-accepted; exceptions are batched at drain points.** A **drain point**
is a moment at which the queued exception closings are put to the user. At the decay-detection
scan (run at turn close, finding the threads whose engagement has decayed) each candidate is
classified:

- **Exception** — the thread is anchor-rich (≥ 6
  anchors). This threshold is a provisional
  value pending real-use data.
- **Routine** — everything else.

A **routine** closing is applied without asking: the summarizer's summary, `resolution=resolved`,
the same frontmatter + thread registry write and demotion out of the active and dormant sets an
accepted ack performs, and ONE user-visible
line, `closed thr_N: <display name> — <summary>` (the id leads because `/back-to <thr_id>` is the
revision path). It rides the turn transaction already open around turn close ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)) and adds no
commit path of its own. **The revision path for a wrongly-summarized routine closing is
re-engagement** (`/back-to`, [§4.2](../../4-user-surface/4.2-session-commands/index.md)): it returns the thread to `active`, and its next close re-drafts
the summary.

An **exception** closing is **queued** silently: no summarizer call, no question, one
`retire.pending` event (the `retire.*` events record closings). The queue is **derived, never stored** — a pending closing is a
decay-eligible thread that classified as an exception — so it is crash-safe and correct across
restarts. It is drained with the full interactive closing dialog at **drain points only**, and on
demand via `/closures` ([§4.2](../../4-user-surface/4.2-session-commands/index.md)). An opencode session never exits, so there is no clean-exit drain
point; **Open:** U4 — the drain point that takes its place. A non-empty queue is ANNOUNCED in one
user-visible line (`N closure(s) pending review — /closures`) when an opencode session starts and
never put to the user as a question.

Contents:

- [§3.5 The offer names the thread](the-offer-names-the-thread.md)
- [§3.5 Mode parameter](mode-parameter.md)
- [§3.5 Ack-quality instrumentation](ack-quality-instrumentation.md)
