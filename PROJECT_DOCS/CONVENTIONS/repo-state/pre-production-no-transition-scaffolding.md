### Pre-production: no transition scaffolding

Until a release ships, replacing a mechanism means replacing it: no dual-write, no compatibility
shims, no feature flags kept for rollback. Git is the rollback. A home-format migration is still
written when a stored format changes, so existing dev and sim homes keep opening.
