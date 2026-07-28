GH_ROOT := $(shell dirname $$(git remote -v | awk '{print $$2; exit 0;}'))

.PHONY: all build fmt fmt-check test test-be test-fe fe-scope-check test-run cover sim sim-completeness-rung sim-tokenceiling-rung sim-shadow-slow-test integration-test update-dependencies update-agents-dependency clean agents recall-madlibs recall-corpus-fetch recall-corpus-test recall-corpus-sweep-data recall-embed-data

all: build

# DO NOT MANUALLY EDIT vvv - use make update-dependencies
AGENTS_VERSION := v0.6.18
AGENTS_REPO := $(GH_ROOT)/agents.git
AGENTS_DIR := .claude/agents
AGENTS_MARKER := $(AGENTS_DIR)/.git/HEAD
agents: $(AGENTS_MARKER)

$(AGENTS_MARKER):
	@mkdir -p $(dir $(AGENTS_DIR))
	@[[ ! -f $(@) ]] || git -C $(dir $(AGENTS_DIR)) fetch --tags $(AGENTS_REPO)
	@[[ -f $(@) ]] || git -C $(dir $(AGENTS_DIR)) clone $(AGENTS_REPO)
	@git -c advice.detachedHead=false -C $(AGENTS_DIR) checkout $(AGENTS_VERSION)
	@(cd $(dir $(AGENTS_DIR)); [[ -d commands/. ]] || ln -sv agents/commands .)

update-agents-dependency: agents
	# update to the latest tagged version
	@git -C $(AGENTS_DIR) fetch --tags
	@git -C $(AGENTS_DIR) tag --sort=committerdate  | tail -1 | xargs git -C $(AGENTS_DIR) -c advice.detachedHead=false checkout
	# update Makefile with the new version tag.
	@(\
	  VTAG=$$(git -C $(AGENTS_DIR) describe --tag) && \
		sed "s/^AGENTS_VERSION := $(AGENTS_VERSION)/AGENTS_VERSION := $$VTAG/" Makefile > Makefile.tmp && \
		mv -f Makefile.tmp Makefile && \
		echo "AGENTS_VERSION: $(AGENTS_VERSION) -> $$VTAG" \
	)

BINDIR := bin

# GOPKGS is the set of Go package roots. All packages live under cmd/ and
# internal/; test/ holds NO Go code (rundata, python tools, and the
# perms-restricted api_keys dir). A bare `./...` makes the go toolchain
# readdir() every directory for package discovery — including test/api_keys
# (mode 0700, benn-owned), which fails the walk for any non-owner before a
# single package compiles. Scoping to the real roots covers 100% of the code
# and never descends into test/, so the keys stay locked down with no perms
# relaxation. Use $(GOPKGS), not ./..., in every vet/test/cover target.
GOPKGS := ./cmd/... ./internal/...

# GOSRCDIRS scopes directory WALKS to the Go source roots, mirroring the GOPKGS
# rationale: gofmt (and git's untracked-file scan) walk directory trees, so
# pointing either at test/ would descend into test/api_keys (mode 0700,
# benn-owned) and fail the walk for a non-owner. cmd/ and internal/ hold 100% of
# the Go code; test/ holds none. Used by fmt/fmt-check and by the fe-scope-check
# untracked-file enumeration. The pinned toolchain in go.mod keeps gofmt output
# identical across machines/agents.
GOSRCDIRS := cmd internal

# build is the compile EDIT GATE — a .PHONY target (declared above) that ALWAYS
# recompiles. It is deliberately NOT a $(BINDIR)/personant file target: a
# file target with no prerequisites no-ops once bin/personant exists, so an
# edit-then-`make build` check silently passed against a stale binary. Compile
# is cheap; correctness of the gate beats a stale-file micro-optimization.
build:
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR)/personant ./cmd

# fmt canonically formats every Go source root in place. fmt-check is its
# read-only gate counterpart: gofmt -l lists files that are NOT canonically
# formatted, and a non-empty list fails. fmt-check is wired into `test` (the
# checkpoint gate) below so formatting drift fails the gate — it runs in
# milliseconds, so it fronts the slow go test run (fail fast).
fmt:
	gofmt -w $(GOSRCDIRS)

fmt-check:
	@drift="$$(gofmt -l $(GOSRCDIRS))"; \
	if [ -n "$$drift" ]; then \
	  echo "gofmt drift — these files are not canonically formatted:"; \
	  echo "$$drift" | sed 's/^/  /'; \
	  echo "run 'make fmt' to fix."; \
	  exit 1; \
	fi

update-dependencies: update-agents-dependency
	go mod tidy
	go get -u ./...
	go mod tidy

# recall-madlibs regenerates the derived recall-fidelity query sets
# from the committed templates. Two sets: the hand-crafted C.2/C.3
# templates -> queries.json (consumed by the default `make test`), and
# the Wikipedia-corpus templates -> corpus_queries.json (consumed only
# by the build-tagged corpus tests). Both outputs are .gitignore'd;
# this target is the only supported way to produce them. Python stdlib
# only — no venv, no deps.
RECALL_MADLIBS_DATA := internal/scenarios/testdata/recall_madlibs
recall-madlibs:
	python3 test/tools/madlibs_generate.py
	python3 test/tools/madlibs_generate.py \
	  --templates-dir $(RECALL_MADLIBS_DATA)/corpus_templates \
	  --out $(RECALL_MADLIBS_DATA)/corpus_queries.json

# recall-corpus-sweep-data generates the synonym-depth-stratified query
# sets for the C.6 calibration sweep: corpus_queries_m1..m4.json, each
# restricting columns to their first M cells (M=1 zero-drift canonical,
# M=4 full drift). .gitignore'd derived artifacts.
recall-corpus-sweep-data:
	@for m in 1 2 3 4; do \
	  python3 test/tools/madlibs_generate.py \
	    --templates-dir $(RECALL_MADLIBS_DATA)/corpus_templates \
	    --synonym-depth $$m \
	    --out $(RECALL_MADLIBS_DATA)/corpus_queries_m$$m.json ; \
	done

# recall-embed-data embeds the corpus topic article-texts and every
# corpus query string via the `reaper` provider's /v1/embeddings
# endpoint, writing the .gitignore'd embeddings.json that the
# embedding-recall test consumes. NETWORKED — needs `reaper` reachable.
# Depends on the sweep query sets existing.
recall-embed-data: recall-corpus-sweep-data
	python3 test/tools/embed_corpus.py

# recall-corpus-test runs the Wikipedia-corpus recall-fidelity report
# and the C.6 calibration sweep. The tests ALWAYS COMPILE (part of the
# normal `make test` compile); EXECUTION is opted in here by setting
# PERSONANT_CORPUS_TESTS — without it they skip, so they are not part of
# the `make test` run. Run -v to see the per-topic report and the
# synonym-depth × threshold calibration matrix.
recall-corpus-test: build recall-madlibs recall-corpus-sweep-data
	PERSONANT_CORPUS_TESTS=1 go test -run Corpus ./internal/scenarios/... --count=1

# recall-corpus-fetch is a HEAVYWEIGHT, NETWORKED mining operation —
# NOT part of `make test` and NOT a pre-commit step. It fetches ~150
# Wikipedia articles (minutes of wall-clock, polite rate limiting) and
# rewrites the committed corpus snapshot at
# internal/scenarios/testdata/corpus/corpus.json. Run it only to
# refresh or extend the recall-fidelity corpus, then commit the result.
recall-corpus-fetch:
	python3 test/tools/wikipedia_corpus.py

# sim-shadow-slow-test runs the opt-in SLOW sim rungs gated by
# testsupport.RequireSlowSim (PERSONANT_SLOW_SIM_TESTS): the 14d arm of
# TestShadowLayerB_ReverseDivergence, plus the fixed-24h #98 embedding
# head-to-head machinery rungs (#94 R4 suite-budget move). All ALWAYS COMPILE
# (part of the normal `make test` compile) but their wall-clock would blow
# `make test`'s 30m sim-package timeout budget, so EXECUTION is opted in here.
# Without the env var they skip and only the fast default-suite rungs run.
# -timeout 0 disables go test's default ceiling for these deliberate, watched
# long rungs (a runaway is the user's to Ctrl-C).
sim-shadow-slow-test: build recall-madlibs
	PERSONANT_SLOW_SIM_TESTS=1 go test ./internal/scenarios/sim/ -run 'TestShadowLayerB_ReverseDivergence|TestSimEmbeddingHeadToHead_Machinery|TestSimNoEmbedder_HeadToHeadAbsent' -count=1 -timeout 0 -v

test: build fmt-check recall-madlibs
	go vet $(GOPKGS)
	# -timeout 30m: the sim package's in-suite mock rungs (the 1d TestSim, the
	# multi-day #120 daily-series + #121 day-off harness guards) push that one
	# package past go test's default 10m per-package budget; 30m bounds a genuine
	# hang without failing a healthy long run. Other packages finish in seconds.
	go test $(GOPKGS) --count=1 -timeout 30m

# test-be is the BACK-END/SUBSTRATE checkpoint gate and is EXACTLY `test` — the
# full suite. The naming pair test-fe/test-be is deliberately NOT symmetric in
# cost, because the dependency graph is not symmetric: cmd/ and internal/chat/
# are a LEAF (nothing imports them), so a front-end-only change cannot regress
# the substrate and gets a cheap scoped target. The front end DOES import the
# substrate, so a substrate change can regress the front end and must run
# everything. There is no back-end saving to be had; `test-be` exists only so
# the fast target has an unambiguous counterpart to name. `test` is kept as the
# primary spelling (habits, AGENTS.md, tooling) and this is a pure alias.
test-be: test

# FE_SCOPE_PREFIXES are the path prefixes a front-end-only change may touch.
# internal/version/ is in the set on purpose: every front-end change bumps
# version.FrontEnd (AGENTS.md front-end bump contract), so a "cmd/ + chat/ only"
# guard would reject every legitimate front-end commit. internal/version is also
# imported by substrate packages — see FE_TESTPKGS for how that is covered.
FE_SCOPE_PREFIXES := cmd/ internal/chat/ internal/version/

# FE_TESTPKGS is the package set `test-fe` runs: the front-end leaf itself plus
# the CHEAP substrate importers of internal/version, as insurance against a
# version const bump breaking a substrate assertion (eventlog 1.2s, memops 2.2s,
# store 14.5s — all seconds). Measured total is ~15-16s versus ~29 minutes for
# the full suite: internal/store dominates and runs in parallel with the rest.
#
# ./internal/memops is EXACT — deliberately NOT ./internal/memops/..., because
# the /... form pulls in internal/memops/fileadapter, whose tests are 229
# SECONDS on their own and are pure substrate. Do not "fix" this to /...; the
# whole point of the target is that it costs seconds.
FE_TESTPKGS := ./cmd/... ./internal/chat/... ./internal/version/... \
               ./internal/eventlog/... ./internal/store/... ./internal/memops

# fe-scope-check is the MECHANICAL guard that makes `test-fe` safe to trust: it
# refuses if the working tree contains any changed .go file outside
# $(FE_SCOPE_PREFIXES), so scope classification is a property of the diff rather
# than an agent's or a human's self-assessment. A clean tree passes (running the
# fast target on an unmodified checkout is legitimate).
#
# Changed set = `git diff --name-only HEAD` (staged + unstaged + deletions)
# plus untracked files, filtered to *.go. The untracked scan is scoped to
# $(GOSRCDIRS) so it never descends into test/api_keys (mode 0700, benn-owned —
# see the GOSRCDIRS comment); test/ holds no Go code, so nothing is missed.
#
# LIMITATION, stated rather than left implicit: the guard inspects GO SOURCE
# ONLY. A Makefile, doc, testdata, or go.mod edit does not trip it — those are
# either build tooling (this change), covered by the mechanical-diff exception,
# or the caller's judgment. It is a Go-regression scope guard, not a
# whole-tree change detector.
fe-scope-check:
	@changed="$$( { git diff --name-only HEAD; \
	                git ls-files --others --exclude-standard -- $(GOSRCDIRS); } \
	              | grep '\.go$$' | sort -u || true )"; \
	outside=""; \
	for f in $$changed; do \
	  ok=""; \
	  for p in $(FE_SCOPE_PREFIXES); do \
	    case "$$f" in $$p*) ok=1 ;; esac; \
	  done; \
	  [ -n "$$ok" ] || outside="$$outside $$f"; \
	done; \
	if [ -n "$$outside" ]; then \
	  echo "make test-fe REFUSED — changed Go files outside the front-end scope:"; \
	  for f in $$outside; do echo "  $$f"; done; \
	  echo "front-end scope is: $(FE_SCOPE_PREFIXES)"; \
	  echo "the front end IMPORTS the substrate, so a substrate change is not covered"; \
	  echo "by the fast target. run 'make test' (== 'make test-be', the full suite)."; \
	  exit 1; \
	fi

# test-fe is the FRONT-END checkpoint gate: the scoped, ~10s counterpart to
# `test` for a change confined to $(FE_SCOPE_PREFIXES). Safe by STRUCTURE, not
# by judgment — cmd/ and internal/chat/ are a dependency leaf, and
# fe-scope-check mechanically refuses anything else. Read the test-be comment
# above before assuming the reverse direction works; it does not.
#
# Mirrors `test`'s build + fmt-check prerequisites, but NOT recall-madlibs: the
# generated mad-libs query set is substrate-test input and no package in
# $(FE_TESTPKGS) reads it.
test-fe: build fmt-check fe-scope-check
	go vet $(FE_TESTPKGS)
	go test $(FE_TESTPKGS) --count=1

# test-run is the EDIT GATE — the counterpart to `test` (the CHECKPOINT GATE).
# `make test` runs the whole suite, including the sim package's multi-day mock
# rungs (minutes); it is for ONE run before committing a checkpoint, NOT for
# re-running between every edit. To verify a specific edit landed, run only the
# touched test(s): PKG selects the package, RUN a -run regexp. Seconds, not
# minutes. (Compile-only? `make build`.)
#   make test-run PKG=./internal/scenarios RUN=TestDayOffThroughHarness
#   make test-run PKG=./internal/recall/scoring RUN='TestScanChunks'
# PKG/RUN reuse the bench vars (defaulted below); pass PKG explicitly.
test-run: build recall-madlibs
	go test $(PKG) -run '$(RUN)' -count=1 -timeout 30m

# bench runs benchmarks only (no unit tests) for a package selected by
# PKG, with a regexp selected by BENCH. Defaults target the recall hot
# path the #93 frontmatter cache accelerates. allocs/op is the headline:
# warm cache.LoadAll should sit far below the uncached
# LoadAllThreadFrontmatter baseline.
#   make bench
#   make bench PKG=./internal/recall/scoring BENCH=.
PKG ?= ./internal/memops/fileadapter
BENCH ?= BenchmarkProposeRecall
RUN ?= .
bench: build
	go test $(PKG) -run '^$$' -bench '$(BENCH)' -benchmem --count=1

# cover reports aggregate test coverage across all packages. -coverpkg
# instruments every package for every test binary, so the number
# reflects how much of the codebase the WHOLE suite exercises — not
# just each package's own tests (cross-package scenario tests count).
# The final `total:` line is the headline percentage; for a
# line-by-line view run `go tool cover -html=cover.out`. cover.out is
# a .gitignore'd derived artifact. Build-tagged tests (integration,
# the sim rungs past 1d) are not included.
cover: build recall-madlibs
	go test -coverpkg=$(GOPKGS) -coverprofile=cover.out $(GOPKGS) --count=1
	@go tool cover -func=cover.out | tail -1

# sim runs the simulation rung walk — TestSim in internal/scenarios/sim/ at the
# span given by -sim.duration. By DEFAULT (both live toggles false) it is the
# MOCK, deterministic acceptance gate: symbolic-only recall (nil-embedder
# stand-in) + scripted mock responses, all hard gates active. `make test` runs
# this same TestSim at the 1d default; `make sim DURATION=...` walks the rungs
# (1d|1w, <N>d like 30d, or a Go duration like 168h). -timeout 0 disables go test's
# 10-minute default (long rungs run minutes -> ~an hour; a watched, deliberate
# invocation — a runaway is the user's to Ctrl-C).
#
# LIVE_EMBEDDING / LIVE_INFERENCE (default false -> the stand-ins above) opt the
# two live elements in independently — there is NO separate sim-live target
# (#98), just this TestSim with the -sim.live-* flags:
#   make sim                                   # mock acceptance gate (default)
#   make sim LIVE_EMBEDDING=true               # embedding-in-loop (real §3.4 embedder)
#   make sim LIVE_INFERENCE=true DURATION=1d   # inference-in-loop (see cap below)
#   make sim LIVE_EMBEDDING=true LIVE_INFERENCE=true DURATION=1d
# A live run reads the USER-provided, gitignored
# test/rundata/test.{providers,config}.toml, resolves the selected chat +
# embedding providers, and FAILS (not skips) if those files are missing or the
# endpoint is unreachable (design §2). Embedding-in-loop keeps the recall oracle
# VALID (symbolic-vs-embedding head-to-head); inference-in-loop is a SHORT
# behavior-validation mode with the oracle gates relaxed (a real model diverges
# from the canned plan) and is REFUSED past a 1-sim-day cap — so with
# LIVE_INFERENCE=true pass a cap-safe DURATION (e.g. DURATION=1d); DURATION
# defaults to 1w for the mock long-haul.
#
# SIM_WRAP wraps the run on Darwin with caffeinate + taskpolicy so a long run is
# not penalized by idle-sleep or background-QoS demotion; process-scoped,
# self-cleaning, empty on non-Darwin.
ifeq ($(shell uname -s), Darwin)
SIM_WRAP := caffeinate -i taskpolicy -t 0 -l 0
else
SIM_WRAP :=
endif
DURATION ?= 1w
LIVE_EMBEDDING ?= false
LIVE_INFERENCE ?= false
sim: build recall-madlibs
	$(SIM_WRAP) go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 \
	  -sim.duration=$(DURATION) -sim.live-embedding=$(LIVE_EMBEDDING) -sim.live-inference=$(LIVE_INFERENCE)

# B1+X4 embedder-enabled acceptance rung (closes the B1 §3.4 recall-completeness
# blind spot + the X4 / X4-PROD whole-request token-ceiling blind spot). The rung
# is SPLIT into two invocation profiles (design Q1), each run in its valid regime
# and BOUNDED — NOT part of the default `make test`, NOT a steady-state ladder
# rung. Both read the USER-provided, gitignored test/rundata/test.{providers,
# config}.toml and FAIL (not skip) on missing/unreachable endpoints. This rung
# MEASURES the X4-PROD violation; it does NOT add the production bound (#127's).
#
#   sim-completeness-rung — the §3.4 completeness FLOOR (B1). Live embedder +
#     MOCK inference (fast, coherent) over several sim-days so the main thread
#     scrolls a probe target into the flush-lag dead zone, where ONLY the bounded
#     lexical floor (#123) can hit. The completeness gate asserts every dead-zone
#     probe surfaced AND that at least one was observed (non-vacuity). Mock
#     inference keeps the recall oracle valid; the span (default 4 sim-days) is
#     capped well below the ladder. Override with DURATION (e.g. DURATION=7d) if a
#     longer scroll-in is needed.
#
#   sim-tokenceiling-rung — the whole-request token CEILING (X4 / X4-PROD). Live
#     inference at the 24h short cap (liveInferenceMaxDuration), with the
#     large-input workload injection ON (verbose tool-result deltas sized to
#     approach the ceiling). The token-ceiling gate asserts max(prompt_tokens) <=
#     the configured ceiling on the FULL assembled request and reports max + P99
#     so a reviewer can confirm the payload approached the ceiling. The 24h cap is
#     enforced by setupLiveElements (a longer span is refused).
B1X4_COMPLETENESS_DURATION ?= 4d
sim-completeness-rung: build recall-madlibs
	$(SIM_WRAP) go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 \
	  -sim.duration=$(B1X4_COMPLETENESS_DURATION) -sim.live-embedding=true -sim.live-inference=false

sim-tokenceiling-rung: build recall-madlibs
	$(SIM_WRAP) go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 \
	  -sim.duration=1d -sim.live-embedding=false -sim.live-inference=true

# integration-test runs the live-inference tests — they require the
# `reaper` provider reachable. The tests ALWAYS COMPILE (part of the
# normal `make test` compile); EXECUTION is opted in here by setting
# PERSONANT_LIVE_TESTS — without it they skip, so bare `make test`
# compiles and skips them, hitting no endpoint. Under the opt-in an
# unreachable/misconfigured endpoint is a FAILURE, not a skip. Override
# the endpoint with PERSONANT_REAPER_URL.
integration-test: build
	PERSONANT_LIVE_TESTS=1 go test $(GOPKGS) --count=1

clean:
	rm -f $(BINDIR)/personant
