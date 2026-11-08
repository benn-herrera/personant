#### The acceptance ladder

A run's **span** is the simulated calendar time it covers, in **days** (most of a day is
not spent working). The ladder is the set of spans run:

- **Smoke spans — `1d` / `7d`.** Regression catch; the default test run exercises `1d`. Not analysis
  spans.
- **Real ladder — `15d → 30d → 60d → 120d`.** Exact doublings. The top span is 120 days.

**Long-span symbolic precision is a corpus-saturation floor, not a regression.** On long spans
symbolic-signal precision/F1 declines and the unresolved-episode rate rises while recall stays flat:
the fixed sim vocabulary lets dormant non-sibling threads (threads outside the true match's simulated topic family) clear the Jaccard threshold alongside the
true match. The decline is bounded; F1 settles. Spans past ~30d partly re-measure this floor.

