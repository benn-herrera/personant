### 3.5 Closing a thread

Engagement decay → summarizer-drafted summary → close as `resolved` → frontmatter + thread registry
update → demotion out of the active and dormant sets. Cold storage ([§3.8](../3.8-cold-storage/index.md)) follows a close under thread registry size pressure (the registry
holding more records than archival allows, [§3.8](../3.8-cold-storage/index.md)).

Engagement decay fires when a thread has gone unengaged for at least `engagement.decay-turns` turns OR at least
`engagement.decay-time` since its `last_engaged` ([§2.6.1](../../2-data-model/2.6-directive-file-format-directives-md/2.6.1-recognized-parameters.md)): `decay-time` measures a thread's neglect against its project's most recent engagement, so an
absence from the project closes nothing; `decay-turns` catches low-frequency engagement in a busy
session. Closing covers the active project's threads.

The summarizer-drafted summary targets 100–150 chars: summary + anchors must fit the registry
slot's budget at realistic thread registry size while letting the model recognize prior engagement
without fetching the body.

**Closing is automatic.** At the decay-detection scan (run at turn close, finding the threads whose
engagement has decayed) each decayed thread is closed with the summarizer's summary as
`resolved`: the frontmatter + thread registry write and the demotion out of the active and dormant
sets. A close is part of its turn: both land or neither ([§4.5.8](../../4-user-surface/4.5-project-identity-and-the-project-s-directory/4.5.8-startup-recovery-after-unclean-shutdown.md)). Nothing closes without a summarizer. A draft that **fails or comes back
empty** closes nothing: it logs `retire.curator-error` (the summarizer-failure event) and is
retried at the next scan, so a persistently failing summarizer leaves its thread open.

**The revision path for a wrongly-summarized close is re-engagement:** a returning engagement
resurrects the thread to `active` ([§3.2](../3.2-topic-tagging-and-engagement-update/index.md)), and its next close re-drafts the summary.
