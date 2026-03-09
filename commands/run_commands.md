# AgentOpt Test Commands

아래 커맨드들은 `/Users/doyechan/Desktop/codes/aiops` 기준으로 작성했다.

## 1. 서버 실행

```bash
cd /Users/doyechan/Desktop/codes/aiops
go run .
```

브라우저에서 아래로 접속:

```text
http://127.0.0.1:8082
```

로그인 계정:

```text
demo@example.com
demo1234
```

## 2. CLI 상태를 테스트용으로 분리

```bash
cd /Users/doyechan/Desktop/codes/aiops
export AGENTOPT_HOME=$PWD/.agentopt-dev
```

## 2-1. Codex SDK runner 설치

로컬 apply 실행기는 이제 `Codex SDK`를 쓰므로 한 번은 아래 설치가 필요하다.

```bash
cd /Users/doyechan/Desktop/codes/aiops
make install-codex-runner
```

설치 확인:

```bash
cd /Users/doyechan/Desktop/codes/aiops
make check-codex-runner
```

정상이라면 아래 한 줄이 나온다.

```text
usage: run.mjs <request.json>
```

## 3. 대시보드에서 CLI 토큰 발급 후 로그인

대시보드에서 `Issue CLI token`을 누른 뒤, 아래 명령 실행:

```bash
go run ./cmd/agentopt login --server http://127.0.0.1:8082
```

프롬프트가 뜨면 대시보드에서 발급한 CLI 토큰을 붙여넣는다.

## 4. 프로젝트 연결

```bash
go run ./cmd/agentopt connect --project demo-repo --repo-path .
go run ./cmd/agentopt projects
```

MVP에서는 여러 프로젝트를 나눠 관리하지 않는다. 연결된 모든 저장소는 같은 shared workspace로 합쳐진다.

## 5. 초기 데이터 업로드

```bash
go run ./cmd/agentopt snapshot
go run ./cmd/agentopt session --recent 1
go run ./cmd/agentopt recommendations
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
    "Inspect the rollout approval flow and summarize the current control path.",
    "Recommend the smallest safe dashboard follow-up patch."
  ]
}
EOF

go run ./cmd/agentopt session --file /tmp/agentopt-session.json
```

## 6. 웹에서 추천 승인 후 CLI 확인

대시보드에서 추천을 `Review` / `Approve` 한 뒤:

```bash
go run ./cmd/agentopt pending
go run ./cmd/agentopt sync
go run ./cmd/agentopt history
go run ./cmd/agentopt impact
```

`sync`와 `apply --yes`는 내부적으로 `tools/codex-runner/run.mjs`를 호출해서 승인된 파일만 수정한다.

## 7. 적용 후 세션 다시 업로드

영향도(`impact`)가 비어 있거나 `Waiting for post-apply sessions.`로 나오면 적용 후 세션을 한 번 더 올린다.

```bash
go run ./cmd/agentopt session --recent 1
go run ./cmd/agentopt impact
```

## 8. 자주 쓰는 점검 커맨드

```bash
go run ./cmd/agentopt projects
go run ./cmd/agentopt status
go run ./cmd/agentopt recommendations
go run ./cmd/agentopt pending
go run ./cmd/agentopt history
go run ./cmd/agentopt impact
go run ./cmd/agentopt audit
```

## 8-1. 모의 approve -> local sync -> rollback 자동 테스트

실제 temp 서버와 temp workspace를 띄워서 아래 흐름을 자동으로 검증한다.

- 대시보드 로그인
- CLI 토큰 발급
- `agentopt login/connect/snapshot/session`
- 추천 apply + approve
- `agentopt sync`
- `agentopt rollback`

실행:

```bash
cd /Users/doyechan/Desktop/codes/aiops
make mock-e2e
```

이 테스트는 real Codex API 대신 stub runner를 써서 백엔드-로컬 에이전트 연계만 검증한다.

## 9. 문제 생겼을 때 복구

현재 CLI가 shared workspace에 연결되어 있는지 확인:

```bash
cat $AGENTOPT_HOME/state.json
go run ./cmd/agentopt projects
```

문제가 있으면 다시 connect해서 shared workspace 상태를 새로 고정:

```bash
go run ./cmd/agentopt connect --project demo-repo --repo-path .
go run ./cmd/agentopt pending
go run ./cmd/agentopt sync
```
