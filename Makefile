GH_ROOT := $(shell dirname $$(git remote -v | awk '{print $$2; exit 0;}'))

.PHONY: all build test cover sim integration-test update-dependencies update-agents-dependency clean agents recall-madlibs recall-corpus-fetch recall-corpus-test recall-corpus-sweep-data recall-embed-data

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

build: $(BINDIR)/personant
	@# simulate expected user privacy settings for keys
	@chmod 700 test/api_keys; chmod 600 test/api_keys/*

$(BINDIR)/personant:
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR) cmd

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

test: build recall-madlibs
	go vet ./...
	go test ./... --count=1

# bench runs benchmarks only (no unit tests) for a package selected by
# PKG, with a regexp selected by BENCH. Defaults target the recall hot
# path the #93 frontmatter cache accelerates. allocs/op is the headline:
# warm cache.LoadAll should sit far below the uncached
# LoadAllThreadFrontmatter baseline.
#   make bench
#   make bench PKG=./internal/recall/scoring BENCH=.
PKG ?= ./internal/memops/fileadapter
BENCH ?= BenchmarkProposeRecall
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
	go test -coverpkg=./... -coverprofile=cover.out ./... --count=1
	@go tool cover -func=cover.out | tail -1

# sim runs the simulation rung walk — TestSim in internal/scenarios/sim/ at the
# span given by -sim.duration. By DEFAULT (both live toggles false) it is the
# MOCK, deterministic acceptance gate: symbolic-only recall (nil-embedder
# stand-in) + scripted mock responses, all hard gates active. `make test` runs
# this same TestSim at the 1d default; `make sim DURATION=...` walks the rungs
# (1d|1w|1m|2m|6m or a Go duration like 168h). -timeout 0 disables go test's
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

# integration-test runs the live-inference tests — they require the
# `reaper` provider reachable. The tests ALWAYS COMPILE (part of the
# normal `make test` compile); EXECUTION is opted in here by setting
# PERSONANT_LIVE_TESTS — without it they skip, so bare `make test`
# compiles and skips them, hitting no endpoint. Under the opt-in an
# unreachable/misconfigured endpoint is a FAILURE, not a skip. Override
# the endpoint with PERSONANT_REAPER_URL.
integration-test: build
	PERSONANT_LIVE_TESTS=1 go test ./... --count=1

clean:
	rm -f $(BINDIR)/personant
