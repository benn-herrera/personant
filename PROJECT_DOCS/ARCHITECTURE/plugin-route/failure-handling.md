### Failure handling

Hooks have no timeout and run sequentially (IO §2.2), so every plugin RPC
carries its own timeout. A Promise-API hook's rejection becomes an Effect defect, so the plugin
catches every failure itself (IO §2.2). The probe plugin fails open: on timeout or error it leaves
the hook input as opencode built it and reports a fallback count.

**Open:** U7 — the production failure policy: fail open (opencode's own composition reaches the
model and memory misses the step) or fail closed (the request is refused). What opencode does when
the `context` hook throws or the service refuses is unestablished (RP §6).

