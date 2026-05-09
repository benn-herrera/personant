GH_ROOT := $(shell dirname $$(git remote -v | awk '{print $$2; exit 0;}'))

.PHONY: all build test integration-test update-dependencies udpate-agents-dependency clean agents

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

# integration-test:

update-attributions:
	claude -p "read AGENTS.md and ARCHITECTURE.md, then read the direct imports section of src/go.mod and update 'Third Party Acknowledgements' at the end of README.md" \
	      --allowedTools "Read,Edit,Write,Glob,Grep" \
				--model sonnet

BINDIR := bin

build: $(BINDIR)/personant

all: build

$(GGML_MARKER):
		@mkdir -p $(dir $(GGML_DIR))
		@[[ -d $(GGML_DIR) ]] && (cd $(GGML_DIR) && git fetch --tags) || git clone $(GGML_REPO) $(GGML_DIR)
		@cd $(GGML_DIR) && git -c advice.detachedHead=false checkout $(GGML_VERSION)

$(BINDIR)/personant:
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR)/personant ./cmd

update-dependencies: update-agents-dependency
	go mod tidy
	go get -u ./...
	go mod tidy

test: build
	go vet ./...
	go test ./... --count=1

clean:
	rm -f $(BINDIR)/personant
