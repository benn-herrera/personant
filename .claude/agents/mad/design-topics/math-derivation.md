---
#
# !GENERATED! from templates/agents/mad/design-topics/math-derivation.tmpl.md and templates/shared-chunks.toml — edit those. DO NOT HAND EDIT THIS FILE.
# !TUNING! family=templates/family/claude.toml seat=none member=none tier=highest=fable,high=opus,medium=sonnet,low=haiku,lowest=haiku stock=highest,high,medium,low,lowest harness=claude
# !BODY-SHA256! 8b36efcbbf9a3a7635fe40d2e8e4750da5d3203c27907dcf52f27365cbefe843
#
---

# TOPIC: Mathematical Derivation Construction

## Domain
Deep, technical mathematics related to physical phenomena from subatomic to cosmological scales.
Build only on the provided axiom set — the exclusive source of foundational truth for this construction. External results (GR, QED, SM constants, established theorems) are legitimate context and never load-bearing on their own: an appeal to one must be justified against the problem at hand. A document specifying the axiom set will be provided alongside the open problem statement.

## Constructive Derivation, Adversarially Defended

State the physical principle invoked at each step, the equations it produces, and the boundary
conditions that close it. Cite specific axioms or already-derived consequences for every non-trivial
assertion.
Mark any step that depends on a result the framework asserts but does not derive in the supplied corpus. A marked step is conditional and surfaces as a candidate for under-determination diagnosis.

### Attack Surface

Identify gaps, unjustified leaps, circular reasoning, sign errors, dimensional inconsistencies,
boundary condition violations, hidden empirical inputs, or implicit reliance on the answer being
known. Defend your own derivation against the same attacks.

## Convergence Criteria

A successful debate terminates in one of these states:

- **(a) Algebraic convergence**: all active participants converge on the same closed-form derivation
  modulo algebraic equivalence. The derivation is the deliverable.
- **(b) Multi-path convergence**: participants reach the same numerical answer via demonstrably independent derivation paths. Both paths are preserved in the deliverable as mutually reinforcing confirmations of the same numerical answer. This is a strong-positive outcome.
- **(c) Under-determination**: all active participants converge on the same statement of why the
  problem cannot be closed from the supplied axioms, and identify the specific additional axiom,
  principle, boundary condition, or empirical input required. The diagnostic statement is the
  deliverable.
- **(d) Unresolved divergence**: participants remain at distinct derivations producing different numerical predictions after the round cap. All candidates are documented and preserved for human arbitration.

## Numerical Validation

Where the problem has a known reference value (e.g., a CODATA quantity, an engine fit value, an experimental measurement), participants may compute their derivation's prediction and compare. A derivation that disagrees with the reference is not automatically wrong — the reference may itself be empirical and the derivation may be predictive — but the disagreement must be acknowledged and explained.

A reference value MUST NOT be load-bearing in the derivation itself. It may be used at the end for validation, and its role must be stated explicitly.
