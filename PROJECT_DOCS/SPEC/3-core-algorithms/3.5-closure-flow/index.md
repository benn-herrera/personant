### 3.5 Closure flow

Trigger detection (engagement decay or an explicit close request, `/done`) → curator-drafted
summary → resolution → frontmatter + spine update → Layer B/C eviction. Deep cold archival ([§3.8](../3.8-deep-cold-archival/index.md))
follows closure under spine cardinality pressure.

**Open:** U4 — the channel for every interactive step below (the exception drain, `/done`, the
`[e]dit` choice, `always` mode, the pending-queue announcement) and the command that triggers each.
The rules for decay, classification, the derived queue and the curator draft are decided.

Engagement decay fires when `last_engaged` exceeds `engagement.decay-turns` OR
`engagement.decay-time` ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)): `decay-time` catches extended user absence, `decay-turns` catches
low-frequency engagement in a busy session.

The curator-drafted summary targets 100–150 chars: summary + anchors must fit the Layer A1 budget at
realistic spine cardinality while letting the model recognize prior engagement without fetching the
body.

**Routine closures are auto-accepted; exceptions are batched at boundaries.** At the decay-detection
scan each candidate is classified:

- **EXCEPTION** — the curator draft is empty or failed, OR the thread is anchor-rich (≥ 6 anchors),
  OR long-engaged (≥ 15 engaged turns, `turn_count`). These criteria are a calibration
  window pending real-use data.
- **ROUTINE** — everything else.

A **routine** closure is applied without asking: the curator's summary, `resolution=resolved`, the
same frontmatter + spine write and Layer B/C eviction an accepted ack performs, and ONE user-visible
line, `closed thr_N: <display name> — <summary>` (the id leads because `/back-to <thr_id>` is the
revision path). It rides the turn transaction already open around turn close ([§4.5.8](../../4-user-surface/4.5-project-identity-and-project-root/4.5.8-startup-recovery-after-unclean-shutdown.md)) and adds no
commit path of its own. **The revision path for a wrongly-summarized routine closure is
re-engagement** (`/back-to`, [§4.2](../../4-user-surface/4.2-memory-native-commands/index.md)): it returns the thread to `active`, and its next close re-drafts
the summary.

An **exception** closure is **queued** silently: no curator call, no prompt, one `retire.pending`
event. The queue is **derived, never stored** — a pending closure is a decay-eligible thread that
classified as an exception — so it is crash-safe and correct across restarts. It is drained with the
full interactive resolver at **boundaries only**, and on demand via `/closures` ([§4.2](../../4-user-surface/4.2-memory-native-commands/index.md)). An opencode
session never exits, so there is no clean-exit boundary; **Open:** U4 — the boundary that takes its
place. A non-empty queue is ANNOUNCED in one user-visible line (`N closure(s)
pending review — /closures`) when an opencode session starts and never prompted.

Contents:

- [§3.5 The offer names the topic](the-offer-names-the-topic.md)
- [§3.5 Escape hatch](escape-hatch.md)
- [§3.5 Ack-quality instrumentation](ack-quality-instrumentation.md)
