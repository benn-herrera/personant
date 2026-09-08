# Show this recipe list — bare `just` runs it (the first recipe in the file).
info:
    @just --list --unsorted

# Alias for `build`, kept for anyone reaching for the conventional name.
all: build

BINDIR := "bin"

# GOPKGS is the set of Go package roots. All packages live under cmd/ and
# internal/; test/ holds NO Go code (rundata, python tools, and the
# perms-restricted api_keys dir). A bare `./...` makes the go toolchain
# readdir() every directory for package discovery — including test/api_keys
# (mode 0700, benn-owned), which fails the walk for any non-owner before a
# single package compiles. Scoping to the real roots covers 100% of the code
# and never descends into test/, so the keys stay locked down with no perms
# relaxation. Use GOPKGS, not ./..., in every vet/test/cover recipe.
GOPKGS := "./cmd/... ./internal/..."

# GOSRCDIRS scopes directory WALKS to the Go source roots, mirroring the
# GOPKGS rationale: gofmt (and git's untracked-file scan) walk directory
# trees, so pointing either at test/ would descend into test/api_keys (mode
# 0700, benn-owned) and fail the walk for a non-owner. cmd/ and internal/
# hold 100% of the Go code; test/ holds none. Used by fmt/fmt-check and by
# the fe-scope-check untracked-file enumeration. The pinned toolchain in
# go.mod keeps gofmt output identical across machines/agents.
GOSRCDIRS := "cmd internal"

# GOTESTCOUNT is the test-cache control knob for the ROUTINE gates (`test`,
# `test-be`, `test-fe`, `test-changed`). EMPTY by default — Go's test cache
# stays ON, the cheapest form of "test what changed": the cache key is
# content-addressed over the package's source, its transitive dependencies,
# the testdata files a test actually reads, and the env vars it calls
# os.Getenv on, so it invalidates exactly when a result could have changed.
# Override for one call with `just GOTESTCOUNT=--count=1 test` (or the same
# spelling as an env var — this variable is a plain default, not
# env-sourced, matching the Makefile's `:=` semantics it replaces). The
# forced-clean path is `just test-nocache`, not a manual override; it is not
# applied to test-race/test-run/sim*/integration-test/recall-corpus-test/
# cover, which keep --count=1 hard-coded because a cached result from those
# would be a footgun (endpoint reachability, scheduling nondeterminism, -v
# output someone needs to actually see).
GOTESTCOUNT := ""

# GOTESTTIMEOUT is the per-package ceiling for every recipe that can reach
# the sim package — `test` (and test-be/test-nocache), `test-run`,
# `test-changed`. internal/scenarios/sim measured 1508.8s on a quiet
# machine; 60m leaves headroom for a contended box without turning a genuine
# hang into a 30-minute-later discovery. test-race deliberately does not use
# this (see its own comment). Override with `just GOTESTTIMEOUT=30m test`.
GOTESTTIMEOUT := "60m"

RECALL_MADLIBS_DATA := "internal/scenarios/testdata/recall_madlibs"

# build is the compile EDIT GATE and ALWAYS recompiles (just recipes have no
# up-to-date/stale-file check the way a Make file-target would, so there is
# no stale-binary footgun to guard against here — that guard was needed in
# Make, not here, and is not a regression to replicate).
#
# It compiles in TWO steps and both are load-bearing. `go build {{GOPKGS}}`
# type-checks EVERY package; building only ./cmd walks the import graph from
# main, so a package nothing imports yet is never compiled and an edit gate
# run against it passes vacuously (internal/term was written and "gated"
# that way once). Multi-package `go build` discards its objects — it is
# exactly a compile check. It runs first so the broadest check fails
# fastest; the linked binary follows, nearly free off the build cache.
#
# Compile bin/personant — the cheapest edit-gate check.
build:
    @mkdir -p {{ BINDIR }}
    go build {{ GOPKGS }}
    go build -o {{ BINDIR }}/personant ./cmd

# Canonically format every Go source root in place.
fmt:
    gofmt -w {{ GOSRCDIRS }}

# Wired into `test` so drift fails the checkpoint gate; runs in
# milliseconds, so it fronts the slow `go test` run (fail fast).
#
# Read-only gate: fails if any file isn't canonically formatted.
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    drift="$(gofmt -l {{ GOSRCDIRS }})"
    if [[ -n "${drift}" ]]; then
      echo "gofmt drift — these files are not canonically formatted:"
      echo "${drift}" | sed 's/^/  /'
      echo "run 'just fmt' to fix."
      exit 1
    fi

AGENTS_REPO := "https://github.com/ave-veritas-et-enodatio/adjagent.git"
AGENTS_DIR := ".claude-temp/adjagent"
agents:
	@mkdir -p .claude-temp
	@[[ -d "{{AGENTS_DIR}}" ]] && git -C "{{AGENTS_DIR}}" pull || git -C .claude-temp clone "{{AGENTS_REPO}}"
	just --justfile "{{AGENTS_DIR}}/justfile" install "$(pwd)"

# NOT the tool for adding one dependency (see add-dependency) — this is
# deliberate whole-graph churn.
#
# Upgrade every dependency to its latest version (go get -u ./...).
update-dependencies:
    go mod tidy
    go get -u ./...
    go mod tidy

# Pin ONE vetted module into go.mod/go.sum:
#   just add-dependency example.com/mod/v2@v2.1.2
# update-dependencies is the wrong tool here — its `go get -u ./...` upgrades
# the whole graph, unrelated churn on a commit whose subject is one import.
# The CONVENTIONS.md vetting checklist (release date, importers, deprecation
# status, transitive dep count, license) is a PRECONDITION of running this,
# not something it can check.
#
# Pin ONE vetted module into go.mod/go.sum.
add-dependency mod:
    @[[ -n "{{ mod }}" ]] || { echo "usage: just add-dependency <module>@<version>"; exit 1; }
    go get {{ mod }}
    go mod tidy

# recall-madlibs regenerates the derived recall-fidelity query sets from the
# committed templates: hand-crafted C.2/C.3 templates -> queries.json
# (consumed by the default `just test`), and the Wikipedia-corpus templates
# -> corpus_queries.json (consumed only by the opt-in corpus tests). Both
# outputs are .gitignore'd; this is the only supported way to produce them.
# Python stdlib only — no venv, no deps.
#
# Regenerate derived recall-fidelity query fixtures from committed templates.
recall-madlibs:
    python3 test/tools/madlibs_generate.py
    python3 test/tools/madlibs_generate.py \
      --templates-dir {{ RECALL_MADLIBS_DATA }}/corpus_templates \
      --out {{ RECALL_MADLIBS_DATA }}/corpus_queries.json

# corpus_queries_m1..m4.json, each restricting columns to their first M
# cells (M=1 zero-drift canonical, M=4 full drift). .gitignore'd.
#
# Generate the synonym-depth-stratified query sets for the C.6 calibration sweep.
recall-corpus-sweep-data:
    #!/usr/bin/env bash
    set -euo pipefail
    for m in 1 2 3 4; do
      python3 test/tools/madlibs_generate.py \
        --templates-dir {{ RECALL_MADLIBS_DATA }}/corpus_templates \
        --synonym-depth "${m}" \
        --out {{ RECALL_MADLIBS_DATA }}/corpus_queries_m${m}.json
    done

# Writes the .gitignore'd embeddings.json the embedding-recall test
# consumes, via the `reaper` provider's /v1/embeddings endpoint.
#
# Embed the corpus texts and query strings. NETWORKED — needs `reaper` reachable.
recall-embed-data: recall-corpus-sweep-data
    python3 test/tools/embed_corpus.py

# The tests ALWAYS COMPILE (part of the normal `just test` compile);
# EXECUTION is opt-in via PERSONANT_CORPUS_TESTS (without it they skip, so
# they are not part of `just test`). Run -v to see the per-topic report and
# the synonym-depth x threshold calibration matrix.
#
# Wikipedia corpus recall-fidelity measurement + C.6 calibration sweep (opt-in).
recall-corpus-test: build recall-madlibs recall-corpus-sweep-data
    PERSONANT_CORPUS_TESTS=1 go test -run Corpus ./internal/scenarios/... --count=1

# HEAVYWEIGHT, NETWORKED mining operation — NOT part of `just test` and not
# a pre-commit step. Fetches ~150 Wikipedia articles (minutes of wall-clock,
# polite rate limiting) and rewrites the committed corpus snapshot at
# internal/scenarios/testdata/corpus/corpus.json.
#
# Refresh/extend the recall-fidelity Wikipedia corpus; commit the result.
recall-corpus-fetch:
    python3 test/tools/wikipedia_corpus.py

# Runs the opt-in SLOW sim rungs gated by testsupport.RequireSlowSim
# (PERSONANT_SLOW_SIM_TESTS): the 14d arm of
# TestShadowLayerB_ReverseDivergence, plus the fixed-24h #98 embedding
# head-to-head machinery rungs. ALWAYS COMPILE (part of the normal `just
# test` compile) but their wall-clock would blow test's per-package timeout
# budget, so EXECUTION is opted in here. -timeout 0 disables go test's
# default ceiling — a runaway here is the user's to Ctrl-C.
#
# Run the opt-in SLOW sim rungs (PERSONANT_SLOW_SIM_TESTS).
sim-shadow-slow-test: build recall-madlibs
    PERSONANT_SLOW_SIM_TESTS=1 go test ./internal/scenarios/sim/ \
      -run 'TestShadowLayerB_ReverseDivergence|TestSimEmbeddingHeadToHead_Machinery|TestSimNoEmbedder_HeadToHeadAbsent' \
      -count=1 -timeout 0 -v

# Shared body for `test` and `test-nocache` — ONE recipe, not a copy that
# can drift: count is the cache-control flag threaded through by each
# caller (empty = cached, --count=1 = forced clean).
_test-body count=GOTESTCOUNT: build fmt-check recall-madlibs
    go vet {{ GOPKGS }}
    go test {{ GOPKGS }} {{ count }} -timeout {{ GOTESTTIMEOUT }}

# CHECKPOINT GATE (substrate): fmt-check (drift fails the gate) + go vet +
# go test over {{GOPKGS}} (full suite, incl. multi-day sim rungs). Go's test
# cache is ON by default (GOTESTCOUNT) — an unchanged package returns
# instantly. Run ONCE before a commit, not between edits; see CONVENTIONS.md
# "Two gates."
#
# CHECKPOINT GATE (substrate): full go vet + go test.
test: _test-body

# Run before a push, when a cached PASS is itself under suspicion, or after
# a toolchain/dependency change.
#
# `test` with the test cache DEFEATED — the same full suite, forced clean.
test-nocache: (_test-body "--count=1")

# There is no back-end fast path to have: cmd/ and internal/chat/ are a
# dependency LEAF (nothing imports them) so a front-end change can use the
# cheap scoped gate, but the front end imports the substrate, so a
# substrate change must run everything.
#
# Alias of `test`, named for symmetry with test-fe.
test-be: test

# FE_SCOPE_PREFIXES are the path prefixes a front-end-only change may touch.
# internal/version/ is included because every front-end change bumps
# version.FrontEnd (CONVENTIONS.md's bump contract), and it is also imported by
# substrate packages — see FE_TESTPKGS. internal/shell/ and internal/term/
# qualify by the same leaf rule: nothing outside this set imports them.
# Before adding a further prefix here, verify the leaf property holds:
# `grep -rl personant/internal/<pkg>` must show importers only from within
# this set — widening this list is what makes the guard untrustworthy.
FE_SCOPE_PREFIXES := "cmd/ internal/chat/ internal/version/ internal/shell/ internal/term/"

# FE_TESTPKGS is the package set `test-fe` runs: the front-end leaf plus the
# cheap substrate importers of internal/version, insurance against a
# version-const bump breaking a substrate assertion. Measured ~15-16s total
# vs ~29 minutes for the full suite. ./internal/memops is EXACT, deliberately
# not ./internal/memops/... — the /... form pulls in fileadapter, whose
# suite alone is 229s and pure substrate; do not "fix" this to /....
FE_TESTPKGS := "./cmd/... ./internal/chat/... ./internal/version/... \
                ./internal/shell/... ./internal/term/... \
                ./internal/eventlog/... ./internal/store/... ./internal/memops"

# fe-scope-check is the MECHANICAL guard that makes `test-fe` safe to trust:
# it refuses if the working tree contains any changed .go file outside
# FE_SCOPE_PREFIXES, so scope classification is a property of the diff
# rather than self-assessment. A clean tree passes.
#
# Changed set = `git diff --name-only HEAD` (staged + unstaged + deletions)
# plus untracked files, the latter scoped to GOSRCDIRS so it never descends
# into test/api_keys (mode 0700, benn-owned).
#
# LIMITATION, stated rather than left implicit: this inspects GO SOURCE
# ONLY. A justfile, doc, testdata, or go.mod edit does not trip it — those
# are either build tooling (this change), covered by the mechanical-diff
# exception, or the caller's judgment.
#
# Mechanical scope guard behind `test-fe` — refuses on any non-front-end Go diff.
fe-scope-check:
    #!/usr/bin/env bash
    set -uo pipefail
    changed="$( { git diff --name-only HEAD; \
                  git ls-files --others --exclude-standard -- {{ GOSRCDIRS }}; } \
                | grep '\.go$' | sort -u || true )"
    outside=""
    for f in ${changed}; do
      ok=""
      for p in {{ FE_SCOPE_PREFIXES }}; do
        case "${f}" in "${p}"*) ok=1 ;; esac
      done
      [[ -n "${ok}" ]] || outside="${outside} ${f}"
    done
    if [[ -n "${outside}" ]]; then
      echo "just test-fe REFUSED — changed Go files outside the front-end scope:"
      for f in ${outside}; do echo "  ${f}"; done
      echo "front-end scope is: {{ FE_SCOPE_PREFIXES }}"
      echo "the front end IMPORTS the substrate, so a substrate change is not covered"
      echo "by the fast target. run 'just test' (== 'just test-be', the full suite)."
      exit 1
    fi

# CHECKPOINT GATE (front-end-only change): the scoped ~15s counterpart to
# `test`, safe by STRUCTURE (fe-scope-check mechanically refuses anything
# outside FE_SCOPE_PREFIXES), not by judgment. Mirrors test's build +
# fmt-check prerequisites but NOT recall-madlibs — no package in
# FE_TESTPKGS reads the generated mad-libs query set.
#
# CHECKPOINT GATE (front-end-only change): scoped go vet + go test, ~15s.
test-fe: build fmt-check fe-scope-check
    go vet {{ FE_TESTPKGS }}
    go test {{ FE_TESTPKGS }} {{ GOTESTCOUNT }}

# Seconds, not minutes. RUN defaults to running everything in PKG.
#   just test-run ./internal/scenarios TestDayOffThroughHarness
#   just test-run ./internal/recall/scoring TestScanChunks
#
# EDIT GATE — run only the touched test(s) instead of the full checkpoint suite.
test-run pkg run='.': build recall-madlibs
    go test {{ pkg }} -run '{{ run }}' -count=1 -timeout {{ GOTESTTIMEOUT }}

# RACEPKGS: packages that earn a place under the race detector — one
# launches a goroutine in non-test code, or its own tests run >=2 goroutines
# against shared state. -race instruments every package linked into a test
# binary, so a single-goroutine package buys nothing.
#   internal/chat            progress ticker + control.go signal goroutine
#                             + term's pump, three goroutines over one
#                             session's state
#   internal/term             the terminal arbiter: one mutex serializing
#                             every emitted byte, contended by the three
#                             producers above
#   internal/recall/measure  single indexer goroutine, atomic.Pointer
#                             snapshot swap, job channel
#   internal/model            SSE stream reader closeOnce/closeMu; mock mu
#   internal/eventlog         package-level writeMu serializing appends
#   internal/metrics          Run.mu behind the metric series
#   internal/log               global atomic.Pointer logger + sync.Once +
#                             emit mutex, written from every goroutine above
#   internal/scenarios        the memtel heap-watchdog goroutine — EXACT,
#                             not /..., see the sim exclusion below
#   internal/shell             §4.4 trailer-reader goroutine; Runner.pgid
#                             written on the REPL goroutine, read from the
#                             signal handler
# EXCLUDED so nobody "fixes" this later by broadening it:
#   internal/scenarios/sim is ~1605s unraced; -race costs 2-10x, putting
#     this in the hours for concurrency already covered above through a
#     slower harness.
#   internal/memops/fileadapter has real shared state but a 229s
#     single-goroutine suite; it IS instrumented, as a linked dependency of
#     the packages above under their concurrent tests.
#   internal/turn, internal/store, internal/index, internal/crashpoint:
#     checked and rejected — no non-test goroutine, no concurrent test.
# Never point this at test/ — test/api_keys is mode 0700 and benn-owned.
RACEPKGS := "./internal/chat/... ./internal/recall/measure/... ./internal/model/... \
             ./internal/eventlog/... ./internal/metrics/... ./internal/log/... \
             ./internal/shell/... ./internal/term/... ./internal/scenarios"

# LIST is test-changed's print-only escape hatch (see its comment below).
LIST := env_var_or_default("LIST", "")

# DIAGNOSTIC, not a gate — run deliberately when you touch concurrent code
# (new goroutine, shared field, lock, channel, ticker). -count=1 defeats the
# test cache (a cached PASS proves nothing about a run that never
# happened). -timeout 30m is a LITERAL, not GOTESTTIMEOUT: RACEPKGS
# excludes the sim by construction, so the whole run is ~70s and 30m is
# already 25x that — inheriting the 60m sim-package raise would buy no
# margin this recipe lacks.
#
# DIAGNOSTIC (not a gate): -race over RACEPKGS, ~70s.
test-race: build recall-madlibs
    go test -race {{ RACEPKGS }} -count=1 -timeout 30m

# DIAGNOSTIC/convenience, same category as test-race — NOT a gate and not a
# third scoping rule; a green test-changed does not satisfy the checkpoint
# gate (front-end-only -> test-fe; anything else -> test, unchanged). What
# it buys is fast ITERATION: only the packages the working tree could have
# affected, so a leaf-package change doesn't drag the 1625s sim package
# along.
#
# Selection: (1) changed paths = `git diff --name-only HEAD` (staged +
# unstaged + deletions) plus untracked files under GOSRCDIRS; (2) path ->
# owning package by walking up until a directory with *.go is found (so a
# testdata edit resolves to its package, matching how go's test cache
# hashes testdata); (3) reverse-dependency closure via `go list -test`
# (this is what catches a dependent reached only through _test.go imports);
# (4) `go test` over the closure, cached like `test`.
#
# Deliberate handling of the awkward inputs:
#   - clean tree -> selects nothing, says so, exits 0 (not an error, not
#     "run everything").
#   - go.mod/go.sum changed -> selects EVERY package (a dependency-version
#     change can reach anything).
#   - anything outside cmd/internal (justfile, docs, test/tools/*) -> not a
#     selector, but LISTED so the omission is visible.
# `just LIST=1 test-changed` prints the selected set without running it.
#
# DIAGNOSTIC (not a gate): run only the packages the working tree could affect.
test-changed: build recall-madlibs
    #!/usr/bin/env bash
    set -uo pipefail
    changed="$( { git diff --name-only HEAD; \
                  git ls-files --others --exclude-standard -- {{ GOSRCDIRS }}; } \
                | sort -u )"
    forcefull=""; dirs=""; outside=""
    for f in ${changed}; do
      case "${f}" in
        go.mod|go.sum) forcefull=1 ;;
        cmd/*|internal/*)
          d="${f}"
          while [[ -n "${d}" ]] && ! ls "${d}"/*.go >/dev/null 2>&1; do
            case "${d}" in */*) d="${d%/*}" ;; *) d="" ;; esac
          done
          [[ -z "${d}" ]] || dirs="${dirs} ./${d}"
          ;;
        *) outside="${outside} ${f}" ;;
      esac
    done
    if [[ -n "${outside}" ]]; then
      echo "test-changed: changed paths outside {{ GOSRCDIRS }} — not selectors:"
      for f in ${outside}; do echo "  ${f}"; done
    fi
    if [[ -n "${forcefull}" ]]; then
      echo "test-changed: go.mod/go.sum changed — selecting every package."
      sel="$(go list {{ GOPKGS }} | sort -u)"
    elif [[ -z "${dirs}" ]]; then
      sel=""
    else
      sel="$(go list -test -f '{{ "{{" }}.ImportPath{{ "}}" }}{{ "{{" }}range .Deps{{ "}}" }} {{ "{{" }}.{{ "}}" }}{{ "{{" }}end{{ "}}" }}' {{ GOPKGS }} \
             | awk -v ch="$(go list -e ${dirs} | sort -u | tr '\n' ' ')" \
                   -v all="$(go list {{ GOPKGS }} | tr '\n' ' ')" '
                 BEGIN { n=split(ch, A, " "); for (i=1;i<=n;i++) if (A[i]!="") CH[A[i]]=1;
                         m=split(all, B, " "); for (i=1;i<=m;i++) if (B[i]!="") ALL[B[i]]=1; }
                 { base=$1; sub(/\.test$/, "", base); sub(/_test$/, "", base);
                   hit=(base in CH);
                   for (i=2;i<=NF;i++) if ($i in CH) hit=1;
                   if (hit && (base in ALL)) SEL[base]=1; }
                 END { for (k in SEL) print k }' \
             | sort -u)"
    fi
    if [[ -z "${sel}" ]]; then
      echo "test-changed: no changed Go packages — nothing to run."
      exit 0
    fi
    echo "test-changed: selected $(echo ${sel} | wc -w | tr -d ' ') package(s):"
    for p in ${sel}; do echo "  ${p}"; done
    if [[ -n "{{ LIST }}" ]]; then echo "test-changed: LIST=1 — not running."; exit 0; fi
    go test ${sel} {{ GOTESTCOUNT }} -timeout {{ GOTESTTIMEOUT }}

# Defaults hit the recall hot path the #93 frontmatter cache accelerates —
# warm cache.LoadAll should sit far below the uncached
# LoadAllThreadFrontmatter baseline.
#   just bench
#   just bench ./internal/recall/scoring .
#
# Run benchmarks only (no unit tests); pkg/bench_re select the target.
bench pkg='./internal/memops/fileadapter' bench_re='BenchmarkProposeRecall': build
    go test {{ pkg }} -run '^$' -bench '{{ bench_re }}' -benchmem --count=1

# -coverpkg instruments every package for every test binary, so the number
# reflects how much of the codebase the WHOLE suite exercises, not just each
# package's own tests. The final `total:` line is the headline; `go tool
# cover -html=cover.out` for a line-by-line view. cover.out is .gitignore'd.
# Build-tagged tests (integration, sim rungs past 1d) are not included.
#
# Aggregate test coverage report across all packages.
cover: build recall-madlibs
    go test -coverpkg={{ GOPKGS }} -coverprofile=cover.out {{ GOPKGS }} --count=1
    @go tool cover -func=cover.out | tail -1

SIM_WRAP := if os() == "macos" { "caffeinate -i taskpolicy -t 0 -l 0" } else { "" }
DURATION := env_var_or_default("DURATION", "1w")
LIVE_EMBEDDING := env_var_or_default("LIVE_EMBEDDING", "false")
LIVE_INFERENCE := env_var_or_default("LIVE_INFERENCE", "false")

# The simulation rung walk — TestSim at -sim.duration. By DEFAULT (both live
# toggles false) this is the MOCK, deterministic acceptance gate: symbolic-
# only recall + scripted mock responses, all hard gates active. `just test`
# runs this same TestSim at the 1d default; `just sim` walks the rungs
# (1d|1w, <N>d like 30d, or a Go duration like 168h). -timeout 0 disables
# go test's 10-minute default for these long, deliberate, watched runs.
#
# LIVE_EMBEDDING / LIVE_INFERENCE (default false) opt the two live elements
# in independently — there is no separate sim-live recipe (#98):
#   just sim                                          # mock gate (default)
#   just sim LIVE_EMBEDDING=true                      # embedding-in-loop
#   just sim LIVE_INFERENCE=true DURATION=1d          # inference-in-loop
#   just sim LIVE_EMBEDDING=true LIVE_INFERENCE=true DURATION=1d
# (env-var form works too: `DURATION=1d just sim`.) A live run reads the
# user-provided, gitignored test/rundata/test.{providers,config}.toml and
# FAILS (not skips) if missing or unreachable. Embedding-in-loop keeps the
# recall oracle valid; inference-in-loop is a SHORT behavior-validation mode
# (oracle gates relaxed) REFUSED past a 1-sim-day cap — pass DURATION=1d
# alongside it. SIM_WRAP wraps Darwin runs with caffeinate + taskpolicy
# against idle-sleep/QoS demotion; empty on other platforms.
#
# Acceptance simulation rung walk (mock by default; see Sim Knobs above).
sim: build recall-madlibs
    {{ SIM_WRAP }} go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 \
      -sim.duration={{ DURATION }} -sim.live-embedding={{ LIVE_EMBEDDING }} -sim.live-inference={{ LIVE_INFERENCE }}

B1X4_COMPLETENESS_DURATION := env_var_or_default("B1X4_COMPLETENESS_DURATION", "4d")

# B1 rung: the §3.4 completeness FLOOR. Live embedder + MOCK inference over
# several sim-days so the main thread scrolls a probe target into the
# flush-lag dead zone, where only the bounded lexical floor (#123) can hit.
# Asserts every dead-zone probe surfaced, plus non-vacuity (at least one
# observed). Not part of `just test`; reads the same live-endpoint config as
# `sim`. Override span with DURATION (e.g. DURATION=7d).
#
# B1: §3.4 recall-completeness floor rung (live embedder + mock inference).
sim-completeness-rung: build recall-madlibs
    {{ SIM_WRAP }} go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 \
      -sim.duration={{ B1X4_COMPLETENESS_DURATION }} -sim.live-embedding=true -sim.live-inference=false

# X4/X4-PROD rung: the whole-request token CEILING. Live inference at the
# 24h short cap, large-input workload injection ON. Asserts the fully
# assembled request's usage.prompt_tokens stays within the configured
# ceiling and reports max + P99. MEASURES the X4-PROD violation; the
# production bound is separate (#127) work. Not part of `just test`.
#
# X4/X4-PROD: whole-request token-ceiling rung (live inference, 1 sim-day cap).
sim-tokenceiling-rung: build recall-madlibs
    {{ SIM_WRAP }} go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 \
      -sim.duration=1d -sim.live-embedding=false -sim.live-inference=true

# Live-inference tests requiring the `reaper` provider reachable. ALWAYS
# COMPILE (part of the normal `just test` compile); EXECUTION opts in via
# PERSONANT_LIVE_TESTS — without it they skip, so bare `just test` compiles
# and hits no endpoint. Under the opt-in an unreachable/misconfigured
# endpoint is a FAILURE, not a skip. Override the endpoint with
# PERSONANT_REAPER_URL.
#
# Live reaper-endpoint tests (opt-in via PERSONANT_LIVE_TESTS).
integration-test: build
    PERSONANT_LIVE_TESTS=1 go test {{ GOPKGS }} --count=1

# Remove the compiled binary.
clean:
    rm -f {{ BINDIR }}/personant
