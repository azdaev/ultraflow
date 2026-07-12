# Ultraflow — local dev + release build helpers.

.PHONY: dev build test frontend clean changelog

# dev: run the daemon against the on-disk frontend (live edits to web/ show up
# after a `make frontend`). No embedding — fastest iteration.
dev:
	go run ./cmd/ultraflow

# frontend: build the React app into web/dist.
frontend:
	cd web && npm install && npm run build

# build: the self-contained release binary — frontend baked in via `-tags embed`,
# so ./ultraflow is a single file that needs no web/dist alongside it. `go_json`
# routes gin's JSON serialization through goccy/go-json (the fast path).
build: frontend
	go build -tags embed,go_json -o ultraflow ./cmd/ultraflow
	@echo "built ./ultraflow (self-contained) — run it from anywhere"

test:
	go test ./... -race -count=1

# changelog: fill the public (CHANGELOG.md) + private (CHANGELOG.internal.md)
# entries for VERSION from the commits since the last tag, laconically, via the
# Claude Code CLI. Run at a release cut, before tagging: `make changelog VERSION=v0.11.0`.
changelog:
	@scripts/gen-changelog.sh $(VERSION)

clean:
	rm -f ultraflow
	rm -rf web/dist
