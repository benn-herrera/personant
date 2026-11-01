### Auxiliary scripts: Python, stdlib only

No `pip`, no virtualenvs, no lockfiles, and no
exception for one small dependency. **Python 3.9 is the floor** (what macOS's command-line tools
ship): no `match`, no runtime `X | Y` unions (use `typing.Optional`/`Union`), no `zip(strict=)`,
no 3.10+ stdlib APIs. The shipped Go binary never depends on Python; Python may
generate committed or derived fixtures through a runner target.

