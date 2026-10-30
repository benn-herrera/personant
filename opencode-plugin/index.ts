// Personant probe plugin: forwards opencode hooks and session events to the
// in-process memory service (options.url) and applies its answers. A failed
// RPC leaves the hook input as opencode built it and counts one fallback;
// every RPC carries the round trips measured since the last one was sent,
// the cumulative fallback count, and a sequence number taken when the plugin
// observed the hook call or event.

const RPC_TIMEOUT_MS = 30_000
const CHECKPOINT_SUMMARY = "PERSONANT PROBE CHECKPOINT"
const TITLE = "Personant probe"

// Never session.status or session.idle: nothing publishes them.
const EVENTS = [
  "session.text.ended",
  "session.step.ended",
  "session.usage.updated",
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
  "session.forked",
  "session.revert.committed",
  "session.deleted",
]

type RTT = { turn: number; hook: string; ms: number }

export default {
  id: "personant-probe",
  async setup(ctx: any) {
    const url = String(ctx.options.url)
    let seq = 0
    let fallbacks = 0
    let turn = 0
    let rtts: RTT[] = []
    let promptID: string | undefined
    let sampled = false

    const rpc = async (path: string, name: string, s: number, body: Record<string, unknown>): Promise<any> => {
      const sent = rtts
      rtts = []
      const t0 = performance.now()
      try {
        const res = await fetch(url + path, {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ ...body, seq: s, name, rtts: sent, fallbacks }),
          signal: AbortSignal.timeout(RPC_TIMEOUT_MS),
        })
        if (!res.ok) throw new Error(`${path} ${res.status}: ${await res.text()}`)
        const out = await res.json()
        turn = out.turn
        return out
      } catch (err) {
        fallbacks++
        rtts.unshift(...sent) // undelivered; resend with the next RPC
        console.error(`personant-probe: ${name}: ${err}`)
        return undefined
      } finally {
        rtts.push({ turn, hook: name, ms: performance.now() - t0 })
      }
    }

    await ctx.session.hook("prompt", async (e: any) => {
      const s = seq++
      promptID = e.messageID
      await rpc("/prompt", "hook.session.prompt", s, { text: e.prompt?.text ?? "" })
    })

    await ctx.session.hook("context", async (e: any) => {
      const s = seq++
      const start = e.messages.findIndex((m: any) => m.id === promptID)
      const out = await rpc("/context", "hook.session.context", s, {
        messages: e.messages.length,
        turnMessages: start < 0 ? 0 : e.messages.length - start,
        ...(sampled ? {} : { sample: e }),
      })
      sampled = true
      if (!out) return
      if (start < 0) {
        fallbacks++
        console.error("personant-probe: context: this turn's user message is not in the hook input")
        return
      }
      e.system = [{ type: "text", text: out.system }]
      e.messages = [
        ...(out.history ?? []).map((m: any) => ({ role: m.role, content: [{ type: "text", text: m.text }] })),
        ...e.messages.slice(start),
      ]
    })

    await ctx.session.hook("compaction", async (e: any) => {
      const s = seq++
      e.result = { summary: CHECKPOINT_SUMMARY }
      await rpc("/compaction", "hook.session.compaction", s, {})
    })

    await ctx.session.hook("title", async (e: any) => {
      const s = seq++
      e.result = TITLE
      await rpc("/mark", "hook.session.title", s, {})
    })

    await ctx.session.hook("model.request", async (e: any) => {
      const s = seq++
      await rpc("/mark", `hook.session.model.request:${e.kind}`, s, {})
    })

    await ctx.session.hook("experimental.idle", async (e: any) => {
      const s = seq++
      const out = await rpc("/idle", "hook.session.experimental.idle", s, {})
      if (!out?.checkpoint) return
      try {
        await ctx.session.compact({ sessionID: e.sessionID })
      } catch (err) {
        fallbacks++
        console.error(`personant-probe: session.compact: ${err}`)
      }
    })

    await ctx.tool.hook("execute.after", async (e: any) => {
      const s = seq++
      await rpc("/tool", "hook.tool.execute.after", s, {
        tool: e.tool,
        status: e.status,
        input: e.input,
        content: e.status === "completed" ? e.result?.content : undefined,
      })
    })

    // Serial: each event is forwarded before the next is read, so the
    // service sees text and step events before the execution event.
    ;(async () => {
      for await (const ev of ctx.event.subscribe({ types: EVENTS })) {
        const s = seq++
        await rpc("/event", ev.type, s, { data: ev.data })
      }
    })().catch((err) => {
      fallbacks++
      console.error(`personant-probe: event subscription ended: ${err}`)
    })
  },
}
