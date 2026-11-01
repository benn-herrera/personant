#### The acceptance ladder

Rungs are spans of simulated calendar time in **days** (most of a day is
not spent working):

- **Smoke rungs — `1d` / `7d`.** Regression catch; the default test run exercises `1d`. Not analysis
  rungs.
- **Real ladder — `15d → 30d → 60d → 120d`.** Exact doublings. The top rung is 120 days.

**Long-rung symbolic precision is a corpus-saturation floor, not a regression.** On long rungs
symbolic-layer precision/F1 declines and the unresolved-episode rate rises while recall stays flat:
the fixed sim vocabulary lets dormant non-sibling threads clear the Jaccard threshold alongside the
true match. The decline is bounded; F1 settles. Rungs past ~30d partly re-measure this floor.

