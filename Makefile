CONTAINER_BIN ?= $(shell if command -v docker >/dev/null 2>&1; then printf '%s' docker; elif command -v container >/dev/null 2>&1; then printf '%s' container; fi)
CPUS ?= $(shell (nproc --all || sysctl -n hw.ncpu) 2>/dev/null || echo 1)
MAKEFLAGS += --jobs=$(CPUS)

# Run `make` with no target to print the help menu.
.DEFAULT_GOAL := help

ifeq ($(strip $(CONTAINER_BIN)),)
$(error neither docker nor container is available in PATH)
endif

# Command that prints the names of currently running containers, one per line.
# docker uses `ps`; Apple's `container` uses `ls` (whose ID column is the name).
ifeq ($(notdir $(CONTAINER_BIN)),docker)
CONTAINER_RUNNING := $(CONTAINER_BIN) ps --format '{{.Names}}'
else
CONTAINER_RUNNING := $(CONTAINER_BIN) ls --quiet
endif

UID_GID := $(shell id -u):$(shell id -g)

# The image of a stage in images/Dockerfile, by its name, e.g.
# $(call image,node). The images are kept there for Dependabot to update.
image = $(shell images/image.sh $(1))

# Boilerplate for running a one-off node tool against ./web in a container, as
# the host user so written files aren't owned by root. Append the command, e.g.
# `$(NODE_RUN) npx oxlint`.
NODE_ARGS := \
	-v $(CURDIR)/web:/srv \
	--user $(UID_GID) \
	-w /srv \
	-e NPM_CONFIG_CACHE=/srv/node_modules/.npm \
	-e NODE_OPTIONS='--disable-warning=ExperimentalWarning' \
	-e BROWSERSLIST_IGNORE_OLD_DATA=true \
	$(call image,node)
NODE_RUN := $(CONTAINER_BIN) run --rm $(NODE_ARGS)

# The admin bootstrap token of the local server. Its invitation creates the
# first administrator, see admin-invitation.
ADMIN_BOOTSTRAP_TOKEN := local-admin

# Flags shared by the `run` and `watch` backend invocations.
SERVER_FLAGS := \
	-database-url postgres://postgres:password@localhost:5432/postgres?sslmode=disable \
	-log-level debug \
	-admin-bootstrap-token $(ADMIN_BOOTSTRAP_TOKEN) \
	-local-sign-in

# Strava sync uses the Strava API application in STRAVA_CLIENT_ID and
# STRAVA_CLIENT_SECRET, if they're set in the environment, see the README for
# how to create one. Without them, it uses a fake Strava with generated
# activities.
ifneq ($(STRAVA_CLIENT_ID),)
SERVER_FLAGS += -strava-client-id $(STRAVA_CLIENT_ID) -strava-client-secret $(STRAVA_CLIENT_SECRET)
else
SERVER_FLAGS += -fake-strava
endif

# The database tests run against. Each test creates its own database.
TEST_DATABASE_URL := postgres://postgres:password@localhost:5432/postgres?sslmode=disable

# Production asset builds, shared by the standalone targets and by `gen`.
ESBUILD_PROD := go tool esbuild web/index.tsx --minify --bundle --outdir=web/dist --sourcemap --target=es6
TAILWIND_PROD := $(NODE_RUN) npx @tailwindcss/cli --minify -i base.css -o dist/index.css

# Block until postgres accepts TCP queries. pg_isready runs inside the container.
# -h localhost forces a TCP check: the entrypoint's bootstrap server listens only
# on the unix socket, so a default socket check would falsely report ready early.
define WAIT_DB
echo "waiting for postgres on :5432..."; n=0; \
until $(CONTAINER_BIN) exec postgres pg_isready -h localhost -U postgres >/dev/null 2>&1; do \
	n=$$((n+1)); [ $$n -ge 150 ] && { echo "postgres not ready after 30s" >&2; exit 1; }; \
	sleep 0.2; \
done; echo "postgres ready"
endef

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | \
		sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: db
db: ## Start the postgres container, with PostGIS and pgRouting
	@if $(CONTAINER_RUNNING) | grep -qx postgres; then \
		echo "postgres already running"; \
		$(WAIT_DB); \
		$(CONTAINER_BIN) exec postgres psql -U postgres -tAc "select 1 from pg_available_extensions where name = 'pgrouting'" | grep -q 1 || \
			{ echo "the running postgres container has no pgRouting; restart it with make clean db" >&2; exit 1; }; \
	else \
		$(CONTAINER_BIN) build -t trail-tools-postgres test/postgres && \
		$(CONTAINER_BIN) run --rm --name postgres -p 5432:5432 -d --memory 4g -e POSTGRES_PASSWORD=password trail-tools-postgres; \
	fi

.PHONY: grafana
grafana: ## Start Grafana with Tempo, which the server sends traces to, on http://localhost:3000
	@if $(CONTAINER_RUNNING) | grep -qx otel-lgtm; then \
		echo "otel-lgtm already running"; \
	else \
		$(CONTAINER_BIN) run --rm --name otel-lgtm -d --memory 4g -p 3000:3000 -p 4317:4317 grafana/otel-lgtm:latest >/dev/null && \
		echo "Grafana is starting on http://localhost:3000"; \
	fi

.PHONY: sqlc
sqlc:
	go tool sqlc generate

.PHONY: buf
buf:
	go tool buf generate
	go tool buf build api --as-file-descriptor-set -o internal/apidesc/descriptor.binpb

.PHONY: buf-lint
buf-lint:
	go tool buf lint

.PHONY: buf-format
buf-format:
	go tool buf format -w

.PHONY: go-format
go-format:
	grep -L -R "^// Code generated .* DO NOT EDIT\.$$" --exclude-dir=.git --include="*.go" . | xargs go tool gofumpt -w

.PHONY: test
test: db ## Run all tests (starts db)
	@$(WAIT_DB)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race ./...

.PHONY: go-lint
go-lint:
	go tool honnef.co/go/tools/cmd/staticcheck ./...

.PHONY: run
run: db ## Run the backend (starts db)
	@$(WAIT_DB)
	@$(MAKE) --no-print-directory admin-invitation &
	go run . $(SERVER_FLAGS)

.PHONY: admin-invitation
admin-invitation: ## Print where to create the first administrator once the local server is up, if there's no administrator yet
	@for i in $$(seq 1 300); do curl -sf -o /dev/null http://localhost:8080/ && break; sleep 1; done; \
	if ! $(CONTAINER_BIN) exec postgres psql -U postgres -tAc "select 1 from users where is_admin limit 1" 2>/dev/null | grep -q 1; then \
		echo "Open http://localhost:8080 to create the first administrator"; \
	fi

.PHONY: import-osm
import-osm: db ## Import OpenStreetMap data for routing, e.g. make import-osm REGION=liechtenstein PBF=<url or file>
	@$(WAIT_DB)
	go run . import-osm \
		-database-url postgres://postgres:password@localhost:5432/postgres?sslmode=disable \
		-region "$(REGION)" -pbf "$(PBF)" $(if $(BOUNDS),-bounds "$(BOUNDS)")

# The Terraform CI plans and applies with, in images/Dockerfile, where
# Dependabot updates it. Local applies use the same version, downloaded into
# .bin on first use: Terraform refuses to read state written by a newer
# version, so applying with another version breaks either CI's deployments
# or local applies. Terraform can't be installed with go install, as its
# go.mod has replace directives.
TERRAFORM_VERSION := $(shell images/image.sh --tag terraform)
TERRAFORM := $(CURDIR)/.bin/terraform-$(TERRAFORM_VERSION)
TERRAFORM_RELEASE := https://releases.hashicorp.com/terraform/$(TERRAFORM_VERSION)

$(TERRAFORM):
	rm -rf $(CURDIR)/.bin/download && mkdir -p $(CURDIR)/.bin/download && cd $(CURDIR)/.bin/download && \
		zip=terraform_$(TERRAFORM_VERSION)_$$(uname -s | tr '[:upper:]' '[:lower:]')_$$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/').zip && \
		curl -fsSLO $(TERRAFORM_RELEASE)/$$zip && \
		curl -fsSL $(TERRAFORM_RELEASE)/terraform_$(TERRAFORM_VERSION)_SHA256SUMS | grep " $$zip$$" | shasum -a 256 -c && \
		unzip -q $$zip terraform && \
		mv terraform $@
	rm -rf $(CURDIR)/.bin/download

.PHONY: infra-plan
infra-plan: $(TERRAFORM) ## Plan changes to the Google Cloud project, with your gcloud credentials
	GOOGLE_OAUTH_ACCESS_TOKEN=$$(gcloud auth print-access-token) && export GOOGLE_OAUTH_ACCESS_TOKEN && \
		$(TERRAFORM) -chdir=infra init -input=false && \
		$(TERRAFORM) -chdir=infra plan

.PHONY: infra-apply
infra-apply: $(TERRAFORM) ## Apply changes to the Google Cloud project, with your gcloud credentials
	GOOGLE_OAUTH_ACCESS_TOKEN=$$(gcloud auth print-access-token) && export GOOGLE_OAUTH_ACCESS_TOKEN && \
		$(TERRAFORM) -chdir=infra init -input=false && \
		$(TERRAFORM) -chdir=infra apply

.PHONY: infra-output
infra-output: $(TERRAFORM) ## Print Terraform's outputs, e.g. make infra-output NAME=domain_records
	GOOGLE_OAUTH_ACCESS_TOKEN=$$(gcloud auth print-access-token) && export GOOGLE_OAUTH_ACCESS_TOKEN && \
		$(TERRAFORM) -chdir=infra init -input=false >/dev/null && \
		$(TERRAFORM) -chdir=infra output $(NAME)

.PHONY: web-deps
web-deps: ## Install web dependencies
	$(NODE_RUN) npm install

.PHONY: tsc
tsc:
	$(NODE_RUN) npx tsc --noEmit

.PHONY: oxlint
oxlint:
	$(NODE_RUN) npx oxlint --type-aware

# Type check and lint in a single container, which is faster than starting
# one per tool.
.PHONY: web-lint
web-lint:
	$(NODE_RUN) npm run --silent lint

.PHONY: web-format
web-format:
	$(NODE_RUN) npx oxfmt

.PHONY: esbuild
esbuild:
	$(ESBUILD_PROD)

.PHONY: fonts
fonts: ## Copy the self-hosted fonts from web/node_modules into web/dist/fonts
	mkdir -p web/dist/fonts
	cp web/node_modules/@fontsource-variable/lexend/files/lexend-latin-wght-normal.woff2 web/dist/fonts/
	cp web/node_modules/@fontsource/lexend-giga/files/lexend-giga-latin-700-normal.woff2 web/dist/fonts/

.PHONY: tailwindcss
tailwindcss:
	$(TAILWIND_PROD)

# The container rebuilding the CSS in make watch. Killing its `run` client
# leaves it running, so make watch kills it by name when it exits.
WATCH_CSS_CONTAINER := $(notdir $(CURDIR))-watch-css

.PHONY: watch
watch: db ## Run backend + assets with live reload
	@$(WAIT_DB)
	@$(MAKE) --no-print-directory admin-invitation &
	-/usr/bin/env bash -c "\
		trap 'kill %1 %2; $(CONTAINER_BIN) kill $(WATCH_CSS_CONTAINER) >/dev/null 2>&1' EXIT;\
		$(CONTAINER_BIN) kill $(WATCH_CSS_CONTAINER) >/dev/null 2>&1;\
		go tool esbuild web/index.tsx \
			--bundle \
			--outdir=web/dist \
			--sourcemap \
			--target=es6 \
			--watch=forever 2> /dev/null & \
		$(CONTAINER_BIN) run --rm --name $(WATCH_CSS_CONTAINER) $(NODE_ARGS) sh watch-css.sh & \
		go tool github.com/mitranim/gow -s run . $(SERVER_FLAGS) -serve-dir $(CURDIR)/web/dist \
	"

.PHONY: gen
# `buf` has `clean: true`, so `buf generate` briefly deletes the gen/ files that
# esbuild bundles. Keep them off the same parallel batch: depend on the codegen
# (which can run in parallel), then build the assets in the recipe, which only
# runs once all prerequisites have finished.
gen: sqlc buf ## Run all code generation
	$(ESBUILD_PROD)
	$(TAILWIND_PROD)

.PHONY: lint
lint: web-lint buf-lint go-lint ## Run all linters

.PHONY: format
format: go-format web-format buf-format ## Run all formatters

.PHONY: clean
clean: ## Stop the db and Grafana containers
	@for c in postgres otel-lgtm; do \
		if $(CONTAINER_RUNNING) | grep -qx $$c; then \
			echo "stopping $$c"; $(CONTAINER_BIN) stop $$c >/dev/null; \
		else \
			echo "$$c not running"; \
		fi; \
	done
