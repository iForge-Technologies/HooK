# Build the frontend, then a single hub binary with it embedded.
VERSION ?= 0.1.0

.PHONY: build web hub linux agents test dev clean

build: web hub

web:
	cd web && npm ci && npm run build

hub:
	go build -trimpath -ldflags "-s -w" -o bin/hub ./cmd/hub

# Static Linux binaries for a VPS (no CGO: SQLite driver is pure Go).
# Copy bin/agents/* to the hub's HOOK_AGENT_DIR so install.sh can fetch them.
linux: web agents
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/hub-linux-amd64 ./cmd/hub
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/hub-linux-arm64 ./cmd/hub

agents:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o bin/agents/hook-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o bin/agents/hook-agent-linux-arm64 ./cmd/agent

test:
	go test ./...
	cd web && npm run typecheck

# Two terminals: `make dev` (hub on :8080) and `cd web && npm run dev` (UI on :5173).
dev:
	HOOK_DATA=./data-dev go run ./cmd/hub

clean:
	rm -rf bin web/dist
