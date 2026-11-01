# Architecture

How the [spec](SPEC.md) is met.

## Storage layer

- **Write path.** appends then syncs
  and then acknowledges.
- **Read path:** reads the
  latest commit.

## Other

Links back to [the spec](SPEC.md),
"Layout" on the next line.
