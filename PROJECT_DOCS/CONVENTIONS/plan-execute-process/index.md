## Plan & Execute Process

- **`ACTIVE_PLAN.md`** at the repo root — or, when the plan is diced, `ACTIVE_PLAN/` with its
  `entry-point.md`, and no root file — is the one plan under way: decisions confirmed at kickoff,
  work-package boundaries, sequencing and status. Dispatched agents execute out of it; its status
  updates as work packages land. It carries outcomes, their consequences and the principles still
  steering open choices — not decision history, which lives in commits. When the work lands, its
  outcomes move into the contract documents and the file is replaced by the next plan.
- **`ROADMAP_PLANS/`** holds partly-planned work that is not under way: a plan with enough design
  done that someone could pick it up. A plan worth keeping goes there, never into a gitignored tree
  (`mad-design/`, `mad-review/`, `.claude-temp/`). A plan there cites only tracked paths.
  - A plan leaves `ROADMAP_PLANS/` in the same change that makes it the active plan, finishes,
    abandons or supersedes it. A superseding plan first absorbs every section, decision and piece
    of evidence it still relies on, stated as its own, and cites nothing in the plan it replaces;
    then the old plan is deleted.
- Working drafts and half-formed notes are scratch (`.claude-temp/`), not plans.
- **A plan over 800 lines is diced** with `just dice-doc <plan>.md <plan-dir>` into a tree of
  section files (`tools/dicer/README.md`), the monolith is removed, and its work-package
  dispatches cite leaves. A plan at or under 800 lines stays one file.
- **A plan may name a normative page outside the contract documents, and that page outranks the
  plan.** The contract documents are THESIS.md, SPEC.md, ARCHITECTURE.md and CONVENTIONS.md. A plan
  may make a page of its own subject normative — for example, a scoring rubric the contract cites —
  and on that subject the page is the requirement while the plan is the route. Where a plan row and
  the page disagree, the work implements the page and the plan's line is reported as moved, in the
  commit that lands it; the landing does not edit the page, and the contract documents cite it
  rather than restate it. A plan that has such a page names it, so a reader knows which source
  settles a disagreement.
Contents:

- [Post-sprint review](post-sprint-review.md)
- [Probe work](probe-work.md)
- [Plan work rating and dispatch](plan-work-rating-and-dispatch.md)
- [Plan dispatchability](plan-dispatchability.md)
