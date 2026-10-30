# A code KB and a tech-lead agent — notes toward the context problem

*Local design notes. Not for the public repo. Captured 2026-09-13 from a working
session; the evidence cited is from that session's fourteen dispatched rows and the
duplication sweep built at the end of it.*

## BLUF

Most of what reads as "agentic coding chaff" is not a coding-ability problem. It is a
**vision problem**: agents act correctly inside a narrow field of view and violate
project-scale invariants they cannot see. Expert humans given the same field of view
produce the same defects — defensive duplication, local reimplementation, divergent
restatement — which is what a decade of turnover-without-handoff does to a legacy
codebase in purely human projects (Autodesk, Adobe are poster children).

The remedy is not better prompting. It is the thing institutions use for the same
problem: **durable, queryable statements of design state and intent, linked to the code
that carries them**, plus a role that assembles the right slice of that for each piece
of work.

Two artifacts follow:

1. A **code KB** — a derived index of what the code contains, the existing authored
   contract docs, and *edges between them*. The edges are the valuable part: an
   orphaned edge is mechanically detectable, and every myopia defect found in this
   session is a missing edge.
2. A **tech-lead agent** in the seat the main session occupies today — read access to
   the KB for every coder, write access to its *authored* layer gated, dispatches
   carrying query hints rather than bulk context, and an explicit choice of operating
   regime per task.

## Non-goal, stated plainly

**Replacing human attention is not a goal of this design, and is not a thing the design
would achieve if pursued.** Complete human replacement with transformer intelligence is
a perpetual-motion-machine project — it proposes to get out more than the mechanism
puts in. This is not that.

The goal is narrower and achievable: **reduce the self-inflicted decision grind, and
improve the odds on the decisions agents do make**, by giving them the context a
competent human would have demanded before deciding. Every instance below where an
agent produced a better answer than the dispatcher specified came from it having the
relevant evidence in view. Every instance where it produced a worse one came from the
evidence being absent — not from the reasoning failing.

The session's own record is the argument. The most consequential calls of the day were
human: ruling a build mode out of existence, seeing that a category was incoherent
rather than underspecified, and killing a proposed rule that reduced to *have taste*.
A lead agent without a human in the loop would have shipped that rule, because it reads
as reasonable.

## The diagnosis: cumulative myopia

**A rule is honoured completely inside the view window and violated at the scale of the
project.** The local work is not sloppy — it is usually exemplary, which is why review
at the same scale does not catch it.

Instances from one session, all real:

| Defect | Why invisible locally |
|---|---|
| Two full implementations of "spawn a CLI, read its stream, classify the outcome" — 539 and 741 lines | Each internally clean. Neither contained a duplicated line. |
| A category (`leaf-as-index`) named once in SPEC, never defined, applied consistently for 3.5 months | Every site used it correctly. |
| A doctrine stated in four documents | Each author was right where they were writing. |
| Two entry points opening a build, one from a retired era | Each door worked. |
| One word (`row`) naming a plan item and a pipeline step | Each file used it consistently. |
| Exit code 2 meaning two things on one surface | Each assignment was locally sensible. |
| 60 and 53 near-identical sentences across two definition pairs | Sixty sentences is not visible from inside either file. |

The last one was found in 0.29 seconds by a tool that did not exist that morning. It had
survived every review the project had run.

**Corollary that predicts where to look:** defensive duplication concentrates at
**ownership boundaries** — package seams, era seams, tool-versus-driver seams. Every
instance above sits on one. A sweep should weight cross-boundary pairs above
intra-boundary ones.

## Comparative versus evaluative findings

The expensive instrument in use today is adversarial multi-model review. It is
*evaluative* machinery — multiple seats, multiple rounds, a retirement gate — and all of
that exists to adjudicate judgment calls.

But most of what fills its nets is **comparative**: two doors open a build; the charter
has two paths; the doctrine has four homes; the round count has two owners; one field
answers two questions. **Either one idea has two implementations or it does not.** None
of that needs adjudication machinery to *find*. It needs enumeration.

Split the instruments:

- **Comparative sweep** — cheap, mechanical, run every checkpoint. Four questions: what
  concept has two implementations, what constant has two definitions, what rule has two
  homes, what word has two meanings.
- **Evaluative review** — expensive, rare, spent on whether a design is *wrong* rather
  than whether it is *doubled*.

Comparative defects also compound while they sit, so early detection is worth more than
it looks: the two classifiers were merely duplicated until one gained two fault
classifications the other lacked — at which point the same bug was fixed on one path and
live on the other. Catch them while both copies still agree and merging is mechanical
rather than archaeological.

**Built, and working**: `dupe_sweep.py` with `just sweep-prose` / `just sweep-python`.
Candidates, never verdicts. It refuses question four rather than approximating it, and
says what a person would have to supply (a glossary binding each term to one referent).

## The sensor already running

Every dispatched row produces an "outside my lane" observation — a defect in a file it
was not given, a capability already implemented elsewhere, a document sentence its
change falsified. That channel produced the duplicate-classifier finding, from an agent
correctly forbidden to touch it.

It is now a required report field (`Seen and not acted on`), alongside `Behavior deltas`
(scope creep that *landed*, which no other field covered) and `Contract gaps` (a rule the
seat had to be told that its own definition should have carried).

**Nothing aggregates them.** Fourteen rows produced fourteen such sections; they went into
fourteen reports and then into conversational summaries. Aggregating them is nearly free
and is the cross-scale view no individual row has.

## Two operating regimes, chosen per decision

- **Narrow** — specific ask, deliberately minimal response space. *Job on the desk, no
  view out the window.*
- **Broad** — open-ended ask with real decision authority, multi-level context at an LOD
  appropriate to the task.

**They are per-decision, not per-dispatch.** The best rows were narrow overall with one
or two explicitly marked broad zones. The plan that drove them already encodes this
without naming it: **bold marks a decision the row itself must make and record**, and a
row with no bold has no open choice in it.

**The selector**, which otherwise is a vibe: *narrow is safe exactly when the dispatcher
can enumerate the complete response space.* When true, narrow is cheaper and more
reliable. When false, narrow is a **trap** — the box hides the part that does not fit.

A narrow brief was written over a non-narrow problem once this session; the agent
refused rather than fabricating inside the box, which is the good outcome of a bad
regime call. The bad outcome is an agent that satisfies the brief and ships the wrong
thing.

**The LOD stack that worked**, three levels: the immediate fact, the adjacent decision
that makes it matter, and the ruling that bounds it.

## The wish as a diagnostic

A rule that reduces to *have taste* is useless as an instruction and valuable as a
**signal**. When you catch yourself writing one, the wish has named a gap: it marks
precisely the context an agent would need in order to choose well, and does not have.

This session produced one. Faced with an agent that might find a near-fit incumbent and
write a second implementation anyway, the first draft said: *extend a near fit, do not
avoid it — but an extension that makes the incumbent worse for its existing callers is
not reuse.* Two vague directives cancelling into *use judgment*. Deleting it and
substituting a falsifiable report field was the right immediate fix. **Throwing it away
was not** — the wish was pointing at something real: an agent at that decision needs to
know what else exists, and why the incumbent is shaped the way it is.

The procedure, then:

1. Notice the wish.
2. Do not ship it. Substitute whatever falsifiable thing is available now — usually a
   record of the decision rather than a rule about it.
3. **Follow the wish to the mechanism that would actually fill the gap**, and to that
   mechanism's own dependencies. This is the thread to the real answer, and it is
   directly useful in design even though it was useless in a prompt.

A hypothetical system that supplied all of it would produce agents acting with more
taste *without anyone writing the wish down*, because they would structurally have what
the choice requires. **The wish puts the way behind itself without stating it.**
Evidence, small but real: nobody instructed taste about prompt delivery, and the
migration row still kept the right mechanism of two — because its brief happened to put
the replay seam in view. Structural access produced a judgment no instruction could
have.

It also pairs with `Contract gaps` from the opposite side. That field asks the agent
which rule it had to be told; the wish is the dispatcher noticing the same absence from
their end, and usually writing prose at it instead of recording it.

### When the thread ends at a human, and why that matters

**It will not always lead to a mechanism.** Sometimes following the wish ends at *this is
why a person is still required*, and recognising that is what keeps this work from
becoming a search for the philosopher's stone to power the perpetual motion machine
(see the non-goal, above).

The discriminator worth testing: **does the answer already exist somewhere unretrieved,
or does it not exist yet, unmade?**

- **Retrieval problems are mechanisable.** The evidence is in the code, the history, or
  a document, and is merely out of view. Surfacing it converts the wish into an
  answerable query.
- **Decision problems are not.** No amount of context supplies a judgment about what the
  project *should* value, because the answer has not been made yet by anyone.

The trap is that unmade decisions impersonate retrieval problems, because a plausible
answer can always be synthesised — which is exactly how a genie wish fails silently. So
when the thread runs out, the finding is *"this is a decision, and here is who has to
make it"*, and that is a successful outcome of the procedure rather than a failure of it.

## Citations that do not rot

The standing rule against citing documents from code comments treats a symptom. Agents
reach for citations because **the need is real**; what is wrong is the only available
citation form.

**A citation rots because it names a location** — a heading, a section, a line. A stable
id does not: the KB's own claim ids survive reorganization because the id is stable and
the location is *derived* into the index and regenerated, with a verify gate keeping the
derived half honest.

Two consequences:

- "Cite the design doc" becomes possible the moment design statements carry stable ids
  instead of being addressed by position.
- **An orphaned citation becomes mechanically detectable.** This is the property that
  matters. It is exactly what would have caught the undefined category: SPEC named it,
  nothing defined it, no edge from naming to definition, and nothing could notice.

**Cost control, borrowed from the claim-graph work:** only statements that get *cited*
need ids. An equation nobody cites is scaffolding; one cited from a claim is load-bearing
by demonstration. Same here — do not id all of SPEC, id what something points at. The
citations themselves identify the structural statements, and authoring cost lands only
where value already appeared.

## What the code KB actually is

Three layers, two of which already exist:

| Layer | Status |
|---|---|
| **Design state** — what must be true, how this implementation satisfies it | Exists: `SPEC.md`, `ARCHITECTURE.md`, `CONVENTIONS.md` |
| **Forward intent** — what is planned, what was ruled out and why | Exists: `ROADMAP.md` |
| **Code state** — what exists, derived and regenerated | **Missing** |
| **Edges between them** | **Missing, and the valuable part** |

The monolith problem is real and is the KB's home ground: no sophisticated project holds
its full spec and design intent in one reasonable-to-context document. The answer is not
a bigger document or an always-loaded one — it is hierarchy plus **query on demand**,
which is what `.index/` and a navigating agent already do for a corpus.

The queries that earn their keep are the cross-layer ones: *which SPEC statements has
nothing implemented; which ARCHITECTURE claims cite nothing; which code carries a rule no
document states; what already does this thing.*

## The tech-lead agent

The role is real and has a job description, derived from what the main session actually
did across fourteen rows: assemble operating context, choose regime per decision, route
file ownership, aggregate out-of-lane reports, and decide what gets promoted from
brief-carried to durable.

Shape:

- Every coding agent gets **read** access to the KB.
- **Write** access to the authored layer is gated to the lead and seats it explicitly
  delegates.
- Dispatches carry **query hints**, not bulk context — the pointer, not the payload.
- The lead chooses **narrow or broad** per task, using the selector above.

**Deputy, not replacement.** An agent in that seat has *less* room than the main session,
because the main session has a human in it. It can own the mechanical half — ownership
routing, query-hint assembly, report aggregation, regime selection on well-understood
tasks. The rulings that move the design still want a person.

**Gate on derivability, not on the KB as a whole.** The derived layer needs no write gate
at all: it is regenerated, and a hand-edit is already a verify failure — a stronger
guarantee than any approval step. Only the authored layer needs gating. This keeps the
gate small enough to be real rather than a rubber stamp, and mechanical rather than
policy.

**Ownership could be derived rather than assigned.** Every file collision this session came
from hand-partitioning, and the hand-written ownership lists contradicted a standing rule
until patched. If the KB knows which symbols carry which statements, *what does this task
touch* is a query rather than a guess — which is the part of the lead's job that failed
most often.

## Already extractable

Not KB-specific; general instruments that happen to have been built here:

- `dupe_sweep.py` — comparative sweep, candidates not verdicts, two corpora.
- The report fields — `Seen and not acted on`, `Behavior deltas`, `Contract gaps`.
- `incumbent-search` — find the incumbent before writing one, search by behaviour, report
  the search including a negative, and where a near fit was rejected name **what it
  lacked** rather than why. A fact a reader can check, not an unfalsifiable reason.
- **Known-answer testing for an analysis tool.** The sweep was built against a known
  seven-way duplicate in git history. That test caught a silent regression during
  construction: a speed optimisation dropped the seventh site, because a restatement's
  rarest windows are exactly the words it added.
- **Refuse and say what you would need**, rather than approximate badly.
- **Stable id + derived location + verify gate** — the citation mechanism, the most
  transferable idea here.

## Open issues — the R&D, not the blockers

None of these is a reason to hold. They are what the work is *for*, and they sort into
two kinds: questions a build answers, and questions only measurement answers. Knowing
which is which is the useful part — building toward a question that needed a probe wastes
the build, and probing a question a slice would have answered wastes the probe.

### Answered by building a slice and looking

1. **Does the KB toolchain apply to code, or only its mechanism?** Leaves are verbatim
   source; for code the source *is* the source. What transfers may be only
   derived-index + refresh/verify + stable ids — which may be enough. One derived index
   over one package settles it.
2. **Who authors the design-state layer, and at what granularity?** Ids on cited
   statements bounds the cost, but someone still mints and maintains them. Id one
   document's cited statements and the cost is measured rather than argued.
3. **Query interface shape.** A `just` target is invocable by every agent; a skill may be
   too (documented for subagents, unconfirmed for commands). The mechanical half must not
   depend on a model choosing to invoke it.
4. **Half-derivable facts are the trap.** *Which modules depend on which* is derived;
   *which of those dependencies are intended* is authored. The gate belongs between them,
   not around the pair — and the boundary is visible as soon as something tries to derive
   the second one and cannot.
5. **The derivable/authored line moves, and moving it is the lever.** The undefined
   category sat on the authored side for 3.5 months, then moved to derived — which made
   the inconsistency detectable *and* introduced a proxy mismatch. Each migration buys a
   guarantee and costs a commitment to one definition.
6. **Aggregating the out-of-lane reports** — where they land, who reads them, what closes
   one. Cheapest thing on this list; the signal already exists.
7. **Glossary for the fourth question.** One word, two meanings is unanswerable without a
   term-to-referent binding. Authoring one for this project's terms would say whether the
   idea is cheap enough to keep.

### Answered only by measuring

Nothing here about agent *behaviour* is verified — the claims rest on reading and
rendering, not on measured outcomes. Two probes are specified and neither has run. Both
can ride on real dispatches rather than a separate exercise: **every dispatch from here
is a sample**, provided someone scores it.

- **Do the new report fields get filled?** Run a fixed set of coder dispatches carrying a
  planted unrequested side effect and a planted rule-gap in the brief, against the
  definitions before and after, and count how many reports name each. Settles
  `Behavior deltas` and `Contract gaps` together. The shell variant of the same probe:
  recipe edits whose blast radius exceeds the asked-for change — a `pipefail` addition, a
  quoting fix that alters word splitting — and count how many reports name the downstream
  behaviour change at all.
- **Does the near-fit wording change disposition reporting?** Dispatch a coder at a task
  whose capability has a near-fit incumbent in another package, before and after, and
  score the report field: does it name what the incumbent lacked, or give an
  unfalsifiable reason.

A third, smaller, from the architect pass: run that seat over a change with an acceptance
criterion it cannot evaluate — a latency bound — and see whether it asserts a conclusion
or flags the check it could not run. That one decides whether a drafted-and-declined
sentence was needed.

## Sequencing

Get this project to a functional, clean state first, then extract. A defensible
stopping condition, better than "the sweep is clean": **an inference build of both
sample papers completes without stopping at a barrier that turns out to be a toolchain
defect.** That failure happened twice in one night and is what the outstanding plans
address. Hitting it demonstrates the toolchain doing its job end to end — which is the
state worth generalising from.

## Addendum 2026-09-27 — evidence from a second project

*Added by the session agent from the linux-kernel-driver-sandbox sessions of 2026-09-25
and 26: seven coder passes on a code generator (`api_gen`), two cold reviews, one prose
pass, and a two-day design conversation. Same diagnosis, new data, and four things the
notes above do not yet say.*

### The seam is where the defects were, again

Six passes, each by a fresh coder with a narrow brief, each green on its own gates
(container suite, QEMU loop, byte-identical output diff). Two cold reviews then found
five ways a definition could validate clean and still produce broken output. Every one
sat at a seam between passes: a rule enforced in one emitter's validator and not
another's, a literal parsed in two modules with two grammars, a name list one binding
refused and the other did not. No pass was sloppy. The defects were invisible from
inside any of them — the ownership-boundary corollary above, with the boundary being
*which pass owned which module*, not which team.

The instrument that caught them was evaluative (two seats reading the whole package
cold), but most of what it found was comparative: two parsers for one grammar, two
lists for one rule. A per-checkpoint comparative sweep over "which rule has two homes"
would have found four of the five for free.

### Two comparative instruments that earned their keep

- **Byte-identical regeneration as a gate.** Every refactor pass on the generator was
  briefed: generate the real definition's outputs before and after, `diff -r` must be
  empty (or must contain exactly the lines the brief names). A refactor that is not
  byte-identical has a behaviour delta whether or not the coder noticed. Cheap, mechanical,
  and it converted "did this pass change anything it should not have" from a review
  question into a build step. Generalises to anything with a deterministic output.
- **Shape as a test.** After the emitters were made a duck-typed protocol, one test
  asserts every registered module's public surface is exactly the four protocol names,
  and another checks each signature with `inspect`. A fifth emitter that drifts fails
  before it is wired. This is the "orphaned edge is mechanically detectable" property,
  applied to module shape instead of citations.

### The failure direction is systematic, which makes it predictable

Across two days the human reversed the agent's first suggestion on seven design calls.
Listed, the reversals are not random:

| First suggestion | Ruling | Direction of the error |
|---|---|---|
| a `_failure` property so a stub can pick its error | `return {}` | mechanism where a rule would do |
| nil names the enum's zero entry | return `"nil"` | a rule added where one could be removed |
| exact-match-first for bit-flag strings | singles plus stragglers | clever local case over a canonical form |
| an enum class for a distinct offset type | a one-member struct | language feature over the meaning (the struct is bit-identical in all four languages) |
| a static profiler hook | an internal chain, interface unpublished | under-weighting the future cost inside a layer |
| a host-side test path with skip guards | deleted; the container is the only place tests run | a convenience for the agent, not the consumer |
| underscore-prefix every key | prefix only metadata | regularity at the cost of legibility |

The bias is toward the near and the clever: add a mechanism, optimise the case in view,
express the language feature rather than the meaning, spend the consumer's future for
the agent's present. That is the same bias as cumulative myopia, showing up in
*proposals* rather than in code.

Consequence for the wish-as-diagnostic procedure: these were decision problems, not
retrieval problems — no document held the answer — so the thread ends at a human every
time. But the *class* is predictable, so a falsifiable check is available at the
decision point without pretending to supply the judgment: *is this a mechanism where a
rule would do; does it name a language feature or a meaning; who pays for it later.* A
yes on any of those is the trigger to escalate, not a rule about how to decide.

### Capture failed where it was not structural

The design conversation produced roughly thirty rulings across two days. Those that
were made while a coder was being briefed landed in the brief and then in a commit
message. Those made in conversation alone did not land anywhere: a "newtype for offsets"
item discussed on day one was carried only in the transcript summary, and when the
human asked to "replace the roadmap item", no roadmap item existed to replace. The
ruling had never been written. It was recovered only because the same person was in
both conversations.

This is the capture-point problem stated in the notes above, with the cost measured:
a ruling that is not a node at ruling time is lost at the next context boundary. The
mechanism the sandbox already had — an "On deck" list in ARCHITECTURE.md — worked
exactly when the ruling was written into it and failed exactly when it was not. The KB's
capture step has to be *at the moment of ruling*, in the conversation, not at the moment
of dispatch.

### Where human attention stayed, enumerated

The claim that attention is irreducible is true and too coarse to act on. This weekend
it was spent at four points, and at none of them was the activity "read the diff":

1. The contract and the brief, read closely before dispatch.
2. The returned report's specific claims, each checked against the artifact by grep
   (a reserved-name table, a rendered literal, a public-surface list) — minutes per
   pass, and it caught a deleted test and a wrong-headed cast.
3. The cold reviews' *facts*, spot-checked; their judgments weighed against what was
   already ruled.
4. The design calls in the table above.

Everything else was executable: the gates, the diffs, the shape tests. So the
tech-lead deputy's boundary can be drawn concretely: it may own everything that is not
on that list, and item 4 is the one that no amount of KB moves off it. Items 1–3 shrink
as the KB grows (a brief that carries query hints needs less reading; a claim that
cites a stable id checks itself).

### Two rules for coder definitions, proposed

From the grind the human named — hyper-local decisions that should have been questions,
and questions asked in coined vocabulary:

- **A decision that touches a contract, crosses a module boundary, or adds a mechanism
  where a rule might do is not the coder's to make.** It stops, states the two readings
  with file and line, names which document it checked, and asks. This is the escalation
  rule in the house CLAUDE.md applied one level down.
- **A question to the human uses only names that resolve in the repository.** No label
  coined in the work. A thing with no name yet is described by where it lives.

The first converts "how could the agent even consider this" into "it did not, it asked".
The second removes the round trip that makes the ask expensive to read. Neither
removes a taste decision; both remove the tax on reaching one.

### The general form: taste in mechanisms, no brief for the system

Cumulative myopia is not specific to agents. The same shape appears at the scale of an
organisation: a large tech company's designed-churn model — voracious recruiting, RSU
grants that vest fastest early and mature at three years, internal policies that wear
people toward a voluntary exit around year two — is a beautifully engineered mechanism
for its metric. It sorts by remaining energy at exactly the point where the company
would otherwise have to judge it, and the judgment is made by the person, at their own
expense. What it cannot see is that the people it filters out carry the most of *why*,
and the codebase it leaves behind has every author gone before the second version. Its
designer had taste in mechanisms and no brief for the system.

That sentence is the general form of every defect in these notes: a coder duplicating a
classifier, an agent proposing a `_failure` property, a comp committee tuning a vesting
curve. Each honours the variable in view completely and violates an invariant it was
never given. The remedy is the same at every scale — make the invariant visible to the
actor at the point of decision, or route the decision to someone who can see it — and the
KB is that remedy applied at the scale where a build can enforce it.

### A framing that travels

"Every dispatch is a clone, born this minute, with the craft and none of the project
memory; the docs, the code and the brief are the entire interface." It explains every
house rule to a newcomer in one sentence — why the contract moves with the work, why
briefs carry outcome and not history, why a returned report's judgments are weak — and
it is the vision-problem diagnosis above in a form that a hardware team, which already
onboards contractors this way, recognises on hearing.

## Addendum 2026-10-04 — notes toward a comparative instrument for question one

*From a kbase session. Design notes only; nothing built. Question one of the comparative
sweep — what concept has two implementations — has no instrument yet; `dupe_sweep` answers
question three over prose. These may help or be a dead end.*

### The cost line first

The LaTeX KB is expensive at build because its subject is fuzzy: a judgment call per claim
and per edge, then deterministic refresh and verify that cost nothing. Code is not fuzzy
— it is typed and analyzable — so a code KB's derived layer needs no judgment at all, and
the only call left is the authored intent, made by the person ruling at the moment they
rule. **Test for staying cheap: refresh and verify of the code KB spend no inference.**
The moment keeping it honest needs a model call, the LaTeX bill has been reintroduced.

### Function fingerprints as bags over closed vocabularies

A function is summarised as several multisets, each deterministic, incremental per
changed function, and comparable by a dot product over count vectors — an embedding in
the strict sense, hand-built and interpretable: the overlap of two bags says *why* two
functions score alike, and a wrong score is readable enough to see what the vocabulary
misfiled. A learned embedding gives a number and no reason; it comes back only if a known
case survives every bag below.

1. **Canonical signature** — inputs as a multiset of types, order-independent; outputs as
   the return joined with every out-parameter. Plumbing stripped (`ctx`, loggers,
   `user_data`), aliases folded, `error` and `Result` one output shape. It is *necessary*:
   two functions doing one job must have compatible shapes, so a duplicate cannot hide
   outside its bucket, and incumbent search becomes a lookup — compute the signature you
   are about to write, read the bucket — which a coder can be required to run. Stated in
   api_gen's neutral type vocabulary it is cross-language. Primitive-heavy buckets
   (`string → string`) are too large to mean anything, which is the types saying nothing;
   a distinct type where an `int` stood (`boxed_scalar`) shrinks the bucket exactly where
   the meaning was missing. The index rewards the typing discipline wanted anyway.
2. **Effects** — what the function does to the world: external calls, resources touched,
   kinds returned or raised. Shape-independent, so it catches the same job done with a
   different signature (the two spawn-and-classify implementations would collide here).
   Blind to pure functions, which the signature carries.
3. **Interior** — direct internal calls first, no taxonomy needed: two functions calling
   the same mix of the library's own helpers are duplicate candidates on that alone.
   Second, the bag closed transitively through the call graph to the leaf operations a
   taxonomy names (math classes, I/O kinds, string processing, collections), since most
   library functions bottom out in the library itself. Weight by call distance and by
   rarity across the codebase (IDF), or every top-level function converges on "network,
   parse, log, allocate". The taxonomy is authored once, extended as cases arrive — the
   same shape as the glossary question four needs.

**One structural filter the duplication question needs:** a caller legitimately resembles
its callee, so exclude pairs that lie on a path in the call graph. The finding is two
functions with overlapping bags and *no* path between them — siblings in different
packages doing one thing by different routes. Weight cross-package pairs up, per the
ownership-boundary corollary.

**Order of building:** signature bucket + direct-call bag + no-path filter first — a parse
and a fixpoint, no taxonomy; then effects; then the leaf-operation taxonomy, only once a
known duplicate survives the first version. Known-answer tests from git history as with
the prose sweep: the two classifiers and the duplicated validators as positives, and a
pair alike in signature and unlike in interior (filter the "and"s from a sentence versus
a word histogram rendered as JSON — both `string → string`, both effect-free) as the
negative the interior bag must separate.

Candidates, never verdicts; refuse and say what is missing rather than approximate.
