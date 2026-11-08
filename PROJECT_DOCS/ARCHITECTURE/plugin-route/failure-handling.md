### Failure handling

Hooks have no timeout and run sequentially (integration-options report §2.2), so every plugin RPC
carries its own timeout. A Promise-API hook's rejection becomes an Effect defect (a rejected Promise-API hook surfaces as an unhandled failure in opencode's Effect runtime, integration-options report §2.2), so the plugin
catches every failure itself. The prototype plugin fails open: on
timeout or error it leaves the hook input as opencode built it and reports a fail-open count.

**Open:** U7 — the production failure policy, fail open or fail closed (SPEC §1; ROADMAP U7). What opencode does when the `context` hook throws or the service refuses is unestablished (resume probe §6).

