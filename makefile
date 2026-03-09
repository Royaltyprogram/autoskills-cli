VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
AGENTOPT_LDFLAGS = -X 'github.com/liushuangls/go-server-template/pkg/buildinfo.Version=$(VERSION)' -X 'github.com/liushuangls/go-server-template/pkg/buildinfo.Commit=$(GIT_COMMIT)' -X 'github.com/liushuangls/go-server-template/pkg/buildinfo.Date=$(BUILD_DATE)'

run:
	APP_MODE=local go run main.go wire_gen.go

run-cli:
	go run ./cmd/agentopt --help

install-codex-runner:
	cd tools/codex-runner && npm install

check-codex-runner:
	node tools/codex-runner/run.mjs 2>&1 | grep "usage: run.mjs <request.json>"

E2E_BASE_URL ?= http://127.0.0.1:8082
e2e:
	E2E_BASE_URL=$(E2E_BASE_URL) go test -v -count=1 ./e2etest -run TestE2E

mock-e2e:
	go test -v -count=1 ./cmd/agentopt -run TestMockDashboardApprovalTriggersLocalSyncAndRollback

closed-beta-smoke:
	./scripts/closed_beta_smoke.sh

ci-beta:
	go test ./...
	$(MAKE) build
	$(MAKE) beta-cli-bundle

beta-cli-bundle:
	./scripts/build_beta_bundle.sh

print-version:
	@echo $(VERSION)

docker-build:
	docker build -t agentopt-beta .

generate:
	go generate ./data
	go tool wire gen wire.go

build: generate
	go mod tidy -v
	go build -ldflags "$(AGENTOPT_LDFLAGS)" -o=output/server main.go wire_gen.go
	go build -ldflags "$(AGENTOPT_LDFLAGS)" -o=output/agentopt ./cmd/agentopt
