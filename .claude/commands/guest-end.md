---
#
# !GENERATED! from templates/commands/guest-end.tmpl.md and templates/shared-chunks.toml — edit those. DO NOT HAND EDIT THIS FILE.
# !TUNING! family=templates/family/claude.toml seat=none member=none tier=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku stock=highest,high,medium,low,lowest harness=claude
# !BODY-SHA256! 7b3d0aff464ed0f8d6f3478df9fe0f8990ec522c839ac6c029bb1219961519aa
#
---
End the active guest-model session pointer.

## Behavior

1. If `guest-session/active-topic.txt` does not exist, report: `"No active guest session."` and
   stop.

2. Otherwise, read the topic name from `guest-session/active-topic.txt`, then remove the file:
   ```bash
   rm guest-session/active-topic.txt
   ```

3. The session directory `guest-session/<topic>/` (containing `messages.json`, `params.env`, and
   `tmp/`) is **preserved as audit history**. Do not delete it. The user can resume later by running
   `/guest-start <topic>` and choosing **resume**.

4. Confirm to the user:
   `"Guest session '<topic>' ended. Audit log preserved at guest-session/<topic>/messages.json. Use /guest-start <topic> to resume."`
