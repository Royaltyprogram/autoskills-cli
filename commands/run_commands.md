# AgentOpt Test Commands

아래 커맨드들은 `/Users/doyechan/Desktop/codes/aiops` 기준이다.
배포 머신에서는 릴리스 설치 후 `agentopt ...`를 직접 사용하고, 여기의 `go run` 예시는 로컬 개발/테스트용이다.

## 1. 서버 실행

```bash
cd /Users/doyechan/Desktop/codes/aiops
go run .
```

브라우저 접속:

```text
http://127.0.0.1:8082
```

로컬 개발용 로그인 계정:

```text
demo@example.com
demo1234
```

prod-like secret-file 실행:

```bash
cd /Users/doyechan/Desktop/codes/aiops
APP_MODE=prod \
JWT_SECRET_FILE=secrets/agentopt-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE=secrets/agentopt-beta-users.json \
OPENAI_API_KEY_FILE=secrets/agentopt-openai-api-key \
go run main.go wire_gen.go
```

## 2. CLI 상태 분리

```bash
cd /Users/doyechan/Desktop/codes/aiops
export AGENTOPT_HOME=$PWD/.agentopt-dev
```

## 3. 대시보드에서 CLI 토큰 발급 후 로그인

대시보드에서 `Create CLI token`을 누른 뒤:

```bash
go run ./cmd/agentopt login --server http://127.0.0.1:8082
```

프롬프트가 뜨면 대시보드에서 발급한 CLI 토큰을 붙여넣는다.

closed beta 배포에서는 demo 계정 대신 서버 기동 시 `AUTH_BOOTSTRAP_USERS_JSON` 또는 secret file로 베타 사용자 계정을 주입해야 한다.

## 4. 워크스페이스 연결

```bash
go run ./cmd/agentopt connect --repo-path .
go run ./cmd/agentopt workspace
```

MVP에서는 여러 프로젝트를 나눠 관리하지 않는다. 연결된 저장소는 같은 shared workspace로 집계된다.

## 5. 초기 데이터 업로드

```bash
go run ./cmd/agentopt snapshot
go run ./cmd/agentopt session --recent 1
go run ./cmd/agentopt collect
go run ./cmd/agentopt reports
go run ./cmd/agentopt status
```

`session --recent 1`이 안 되면 JSON 파일로 직접 업로드:

```bash
cat > /tmp/agentopt-session.json <<'EOF'
{
  "tool": "codex",
  "token_in": 1800,
  "token_out": 420,
  "raw_queries": [
    "Inspect the current dashboard flow before changing anything.",
    "Summarize where the workflow seems to spend extra steering turns."
  ]
}
EOF

go run ./cmd/agentopt session --file /tmp/agentopt-session.json
```

## 6. 보고서 확인

```bash
go run ./cmd/agentopt reports
go run ./cmd/agentopt status
go run ./cmd/agentopt audit
```

대시보드에서는 아래를 본다:

- latest report cards
- report timeline
- usage analytics
- workspace activity

아무것도 로컬 에이전트에 자동 적용되면 안 된다.

## 7. 후속 세션 업로드

첫 보고서를 읽고 실제 Codex 세션을 더 만든 뒤 다시 업로드:

```bash
go run ./cmd/agentopt session --recent 1
go run ./cmd/agentopt collect --recent 2 --snapshot-mode skip
go run ./cmd/agentopt reports
```

다음 보고서 refresh가 완료되면 최신 사용 패턴이 반영되어야 한다.

## 8. 자주 쓰는 점검 커맨드

```bash
go run ./cmd/agentopt workspace
go run ./cmd/agentopt status
go run ./cmd/agentopt reports
go run ./cmd/agentopt snapshots
go run ./cmd/agentopt sessions --limit 5
go run ./cmd/agentopt audit
```

## 9. 백그라운드 수집

세션 업로드를 계속 유지하려면:

```bash
go run ./cmd/agentopt collect --watch --recent 1 --interval 30m
```

## 10. prod secret-file E2E smoke

ignored local secret files를 사용해서 closed beta prod 경로를 실제로 태운다.

```bash
cd /Users/doyechan/Desktop/codes/aiops
JWT_SECRET_FILE_OVERRIDE=secrets/agentopt-jwt-secret \
AUTH_BOOTSTRAP_USERS_FILE_OVERRIDE=secrets/agentopt-beta-users.json \
OPENAI_API_KEY_FILE_OVERRIDE=secrets/agentopt-openai-api-key \
EXPECT_RESEARCH_MODE=openai_responses_api \
make closed-beta-prod-smoke
```

## 11. 문제 생겼을 때 복구

현재 CLI가 shared workspace에 연결되어 있는지 확인:

```bash
cat $AGENTOPT_HOME/state.json
go run ./cmd/agentopt workspace
```

문제가 있으면 다시 connect해서 shared workspace 상태를 새로 고정:

```bash
go run ./cmd/agentopt connect --repo-path .
go run ./cmd/agentopt reports
go run ./cmd/agentopt status
```
