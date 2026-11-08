### Pre-production: no transition scaffolding

Replacing a mechanism before a release still writes a format migration when a stored format
changes, so existing dev and sim memory stores keep opening.
