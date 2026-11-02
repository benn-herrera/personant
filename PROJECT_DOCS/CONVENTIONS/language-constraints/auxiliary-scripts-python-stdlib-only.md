### Auxiliary scripts: Python, stdlib only

No `pip`, no virtualenvs, no lockfiles, and no
exception for one small dependency. **Python 3.9 is the floor** (what macOS's command-line tools
ship): no `match`, no runtime `X | Y` unions (use `typing.Optional`/`Union`), no `zip(strict=)`,
no 3.10+ stdlib APIs. The shipped Go binary never depends on Python; Python may
generate committed or derived fixtures through a runner target.

**Exception: user-installed tooling.** A Python tool the user has chosen and wired in — installed
into the project virtualenv by a `just` recipe (today `docdicer`, via `_venv`) — is used through
that recipe. Agents never add, remove or upgrade such a tool, its package source or the virtualenv
setup on their own initiative; they propose it to the user instead.
