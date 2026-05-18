GH_ROOT := $(shell dirname $$(git remote -v | awk '{print $$2; exit 0;}'))

.PHONY: all build test cover sim integration-test update-dependencies update-agents-dependency clean agents recall-madlibs recall-corpus-fetch recall-corpus-test recall-corpus-sweep-data recall-embed-data

all: build

# DO NOT MANUALLY EDIT vvv - use make update-dependencies
AGENTS_VERSION := v0.6.5
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
# and the C.6 calibration sweep. Build-tag isolated (recall_corpus) and
# deliberately NOT part of `make test`. Run -v to see the per-topic
# report and the synonym-depth × threshold calibration matrix.
recall-corpus-test: build recall-madlibs recall-corpus-sweep-data
	go test -tags recall_corpus -run Corpus ./internal/scenarios/... --count=1

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

# sim runs the six-month simulation rung walk at a given span — TestSim
# in internal/scenarios/sim/ reads the span from -sim.duration. Span is
# a runtime parameter, no build tags. Deliberately NOT part of `make
# test`: the default test run passes no flag, so TestSim there runs the
# 1-day smoke rung. Run -v to see the logged metrics summary.
# -timeout 0 disables go test's 10-minute default — the longer rungs
# run for minutes to (at 6m) an hour; this is a deliberate, watched
# invocation, so a runaway is the user's to Ctrl-C.
#   make sim DURATION=1w   (1d|1w|1m|2m|6m or a Go duration like 168h)
# Set CLEAN=1 to wipe the whole test/rundata/ forensic-data tree before
# the run, so it begins from an empty directory; otherwise rundata
# accumulates a subdirectory per distinct (seed, duration) across runs.
#   make sim DURATION=1m CLEAN=1
DURATION ?= 1w
CLEAN ?=
sim: build recall-madlibs
	go test ./internal/scenarios/sim/ -run TestSim -count=1 -v -timeout 0 -sim.duration=$(DURATION) $(if $(CLEAN),-sim.clean=true)

# integration-test runs the live-inference tests (build tag
# `integration`) — they require the `reaper` provider reachable.
# Tests skip cleanly when reaper is unreachable; they fail only on a
# real defect. Override the endpoint with PERSONANT_REAPER_URL.
integration-test: build
	go test -tags integration ./... --count=1

clean:
	rm -f $(BINDIR)/personant
