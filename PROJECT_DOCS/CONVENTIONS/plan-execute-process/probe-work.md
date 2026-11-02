### Probe work

A probe is work whose purpose is integration data; a probe plan is a plan made of probe work, and a
probe run is a run whose purpose is integration data.

- Probe work runs on a throwaway branch, never on `main` or an implementation branch.
- A system defect found during probe work is recorded as a ROADMAP item stating the fact and where
  it lives, and is not fixed on the probe branch.
- Probe runs use mock inference only: no live model and no live embedder. The live-inference and
  live-embedding sim modes stay available to every run that is not a probe run.
- **A probe plan is exempt from the checkpoint gate and the post-sprint review.** Its packages gate
  only on the runs that make its data trustworthy, plus `just test-run` for any production package
  they add a file to; its review is a spot check that the data it gathers measures what it claims.

