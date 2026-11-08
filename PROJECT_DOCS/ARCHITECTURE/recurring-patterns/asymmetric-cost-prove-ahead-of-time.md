### Asymmetric cost: prove ahead of time

When the cost of being wrong is far higher than the cost of being right, the bar shifts toward
"prove it."

- Whether this design meets SPEC over months of accumulated memory cannot be judged by inspection. The §9 measurement regime exists
  because "use it and find out across extended accumulated usage and then revise if it's wrong" has
  no graceful recovery — you'd have to either complex-refactor accumulated memory or lose it all.
- Migration cost counts at decision time (e.g. YAML over TOML for thread frontmatter: the
  off-standard gain was modest, the migration cost high). The design takes the standard path
  wherever migration cost is high.

