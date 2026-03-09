run:
	APP_MODE=prod go run main.go wire_gen.go

run-cli:
	go run ./cmd/agentopt --help

E2E_BASE_URL ?= http://127.0.0.1:8082
e2e:
	E2E_BASE_URL=$(E2E_BASE_URL) go test -v -count=1 ./e2etest -run TestE2E

generate:
	go generate ./data
	go tool wire gen wire.go

build: generate
	go mod tidy -v
	go build -o=output/server main.go wire_gen.go
	go build -o=output/agentopt ./cmd/agentopt
