GH_ROOT := $(shell dirname $$(git remote -v | awk '{print $$2; exit 0;}'))

.PHONY: all build test integration-test update-dependencies update-agents-dependency clean agents recall-madlibs

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

$(BINDIR)/personant:
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR)/personant ./cmd

update-dependencies: update-agents-dependency
	go mod tidy
	go get -u ./...
	go mod tidy

# recall-madlibs regenerates the derived recall-fidelity query set
# (Phase C.2). The output is .gitignore'd; this target is the only
# supported way to produce it. Python stdlib only — no venv, no deps.
recall-madlibs:
	python3 test/tools/madlibs_generate.py

test: build recall-madlibs
	go vet ./...
	go test ./... --count=1

integration-test:
	@echo Integration Test TBD

serve-local-api:
	@llama-server --models-dir ~/projects/JIC/models --port 11117 --ctx-size 16384

clean:
	rm -f $(BINDIR)/personant
