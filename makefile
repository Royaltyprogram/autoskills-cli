VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
CRUX_LDFLAGS = -X 'github.com/Royaltyprogram/aiops/pkg/buildinfo.Version=$(VERSION)' -X 'github.com/Royaltyprogram/aiops/pkg/buildinfo.Commit=$(GIT_COMMIT)' -X 'github.com/Royaltyprogram/aiops/pkg/buildinfo.Date=$(BUILD_DATE)'

run:
	APP_MODE=local go run main.go wire_gen.go

run-cli:
	go run ./cmd/crux --help

E2E_BASE_URL ?= http://127.0.0.1:8082
e2e:
	E2E_BASE_URL=$(E2E_BASE_URL) go test -v -count=1 ./e2etest -run TestE2E

closed-beta-smoke:
	./scripts/closed_beta_smoke.sh

closed-beta-prod-smoke: build
	./scripts/closed_beta_prod_smoke.sh

ci-beta:
	$(MAKE) generate
	go test ./...
	$(MAKE) build
	$(MAKE) beta-cli-bundle
	$(MAKE) verify-beta-bundle
	$(MAKE) verify-install-script

verify: ci-beta

beta-cli-bundle:
	./scripts/build_beta_bundle.sh

verify-beta-bundle:
	./scripts/verify_beta_bundle.sh "$(BUNDLE)"

verify-install-script:
	./scripts/verify_install_script.sh "$(BUNDLE)"

build-release-index:
	./scripts/build_release_index.sh "$(RELEASE_DIR)" "$(VERSION_LABEL)"

publish-github-release:
	./scripts/publish_github_release.sh

store-export: build
	./output/crux store-export --output "$(OUTPUT)"

store-import: build
	./output/crux store-import --input "$(INPUT)" --yes

print-version:
	@echo $(VERSION)

docker-build:
	docker build -t crux-beta .

generate:
	go generate ./data
	go tool wire gen wire.go

build: generate
	go mod tidy -v
	go build -ldflags "$(CRUX_LDFLAGS)" -o=output/server main.go wire_gen.go
	go build -ldflags "$(CRUX_LDFLAGS)" -o=output/crux ./cmd/crux
