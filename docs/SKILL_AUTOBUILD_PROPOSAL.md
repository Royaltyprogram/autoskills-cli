# Skill Auto-Build 제안서

## 배경

현재 제품은 사용자의 Codex 세션을 수집/분석해 **관찰형 리포트**를 제공합니다. 다음 단계는 리포트를 사람이 읽고 수동으로 행동을 바꾸는 수준을 넘어서, 시스템이 사용자의 반복 패턴을 학습해 로컬 skill을 **지속적으로 자동 갱신**하는 것입니다.

이번 방향의 핵심은 "스킬을 제안하는 제품"이 아니라 "사용자별 기본 operating skill set을 자동 운영하는 제품"입니다.

- 리포트는 행동 인사이트를 설명하는 화면이다.
- 실제 실행은 서버가 합성한 단일 skill set이 담당한다.
- 사용자는 매번 승인하지 않아도 된다.
- 대신 언제 무엇이 바뀌었는지, 왜 바뀌었는지, 효과가 있었는지는 항상 볼 수 있어야 한다.

즉, 제품 경험을 `read report -> decide -> install`에서 `system learns -> local skill set updates -> report explains the change`로 바꿉니다.

---

## 목표

1. 리포트 인사이트를 사용자별 **단일 canonical skill set**으로 자동 합성
2. 사용자의 개입 없이도 로컬 skill이 주기적으로 최신 상태로 유지
3. 자동화로 생기는 리스크는 승인 UI가 아니라 버전, 검증, 롤백, 제한된 실행권한으로 제어
4. 리포트는 제안 화면이 아니라 "왜 이런 업데이트가 발생했는지"를 설명하는 관측 UI로 전환

---

## 제품 원칙

### 1) Zero-touch by default

기본 경험은 자동입니다. 사용자는 승인하지 않아도 최신 skill set을 받습니다.

### 2) One managed bundle, multiple categorized docs

리포트 항목마다 skill을 따로 생성하지 않고, 하나의 대표 skill bundle만 유지합니다. 다만 표현은 단일 `SKILL.md`가 아니라 카테고리별로 구조화된 여러 개의 md 파일이 더 적합합니다.

예시 경로:

- `$CODEX_HOME/skills/crux-personal-skillset/SKILL.md`
- `$CODEX_HOME/skills/crux-personal-skillset/01-clarification.md`
- `$CODEX_HOME/skills/crux-personal-skillset/02-planning.md`
- `$CODEX_HOME/skills/crux-personal-skillset/03-validation.md`

여기서 `SKILL.md`는 엔트리포인트이자 인덱스 역할만 맡고, 실제 행동 규칙은 카테고리별 md 파일에 분리합니다. 이렇게 해야 diff 가독성, 충돌 제어, 부분 롤백, 카테고리별 품질 측정이 쉬워집니다.

### 3) Invisible execution, visible reasoning

사용자가 매번 수정하지는 않더라도, 최근 변경 요약과 근거는 항상 볼 수 있어야 합니다.

### 4) Safe automation over arbitrary power

자동 업데이트는 강력하지만 위험합니다. 따라서 초기 자동화는 선언형 텍스트 자산만 허용하고, 임의 스크립트 생성/실행은 자동 모드에서 금지하는 것이 맞습니다.

---

## 제안 UX

## 1) 리포트의 역할 변경: 제안 UI가 아니라 "자동 업데이트 로그"

기존 리포트 카드의 CTA 중심 UX 대신, 리포트 상단에 `Skill Set Status` 영역을 둡니다.

표시 예시:

- `Your skill set was automatically updated`
- `version v12 -> v13`
- `changed because requirement clarification failures repeated in 5 sessions`
- `expected impact: fewer premature implementations`

여기서 중요한 것은 사용자가 설치 버튼을 누르는 경험이 아니라, 이미 반영된 변경을 이해하는 경험입니다.

핵심 UI 요소:

- 현재 배포된 skill set 버전
- 최근 변경 3줄 요약
- 변경을 유도한 대표 evidence
- 기대 효과와 실제 효과
- `Pause updates`
- `Rollback`
- `View generated skill bundle`

## 2) 상세 화면: "무엇이 바뀌었는지"에 집중

상세 패널에서는 편집기가 아니라 `change diff` 중심으로 보여줍니다.

예시:

- 추가된 행동 규칙
- 제거된 행동 규칙
- 강화된 금지 패턴
- 이번 변경이 적용된 근거 세션
- 신뢰도와 배포 결정 이유

이 UX는 "직접 skill을 만드는 느낌"보다 "내 에이전트 운영 규칙 묶음이 자동 조정되는 느낌"에 더 가깝습니다.

## 3) 설정 UX: approve-per-change 대신 운영 모드 선택

사용자에게 필요한 제어는 매 변경 승인보다 상위 레벨의 운영 모드입니다.

- `Autopilot`
  - 권장 기본값
  - 검증 통과 시 자동 배포
- `Observe only`
  - 후보 skill set은 계산하지만 로컬 반영은 안 함
- `Frozen`
  - 현재 버전 유지, 자동 업데이트 중지

이 방식이면 사용자는 시스템 설계 철학을 바꾸지 않고도 자동화 강도를 조절할 수 있습니다.

## 4) 설치 UX 제거, 동기화 UX 추가

대시보드 버튼은 `설치`가 아니라 `동기화 상태`를 보여줘야 합니다.

예시:

- `Last synced 8m ago`
- `Pending update: none`
- `Applied version: v13`

CLI는 별도 설치 명령 없이 `crux setup`, `crux collect`, 백그라운드 워처 시점에 최신 skill set manifest를 pull해서 동기화합니다.

## 5) 실패 경험 UX

자동화는 실패했을 때 UX가 중요합니다.

보여줘야 할 상태:

- `candidate generated but not deployed`
- `blocked by low confidence`
- `rolled back automatically due to negative impact`

즉 성공 UX보다도 실패/보류 UX를 먼저 설계해야 합니다.

---

## 기능 구현 제안

## 아키텍처 개요

### A. Report → BehaviorDelta → SkillSetSpec 변환 계층

리포트 하나를 곧바로 스킬 파일로 바꾸지 말고, 먼저 `BehaviorDelta`를 누적한 뒤 최종 `SkillSetSpec`으로 합성합니다.

권장 흐름:

1. 세션 분석 결과에서 행동 문제/개선점을 추출
2. 각 항목을 독립적인 `BehaviorDelta`로 저장
3. 최근 N개 리포트와 효과 데이터까지 반영해 하나의 `SkillSetSpec` 생성
4. `SkillSetSpec`을 버전화하고 배포

예시 스키마:

```json
{
  "schema_version": "skill-set-spec.v1",
  "workspace_id": "ws_123",
  "version": 13,
  "based_on_report_ids": ["rpt_120", "rpt_121", "rpt_122"],
  "documents": [
    {
      "path": "01-clarification.md",
      "category": "clarification",
      "title": "Clarify Before Building",
      "rules": [
        "불명확한 요구사항이 있으면 바로 구현하지 말고 질문부터 한다",
        "가정이 생기면 명시한다",
        "검증 계획을 먼저 제시한다"
      ],
      "anti_patterns": [
        "근거 없는 추정으로 구현 시작",
        "제약 확인 없이 API 설계 확정"
      ],
      "confidence": 0.87,
      "evidence_refs": ["sess_10", "sess_18", "sess_22"]
    }
  ]
}
```

중요한 포인트는 `report -> skill`이 아니라 `reports -> deltas -> categorized canonical skill bundle`이라는 점입니다.

### B. Skill Set Compiler

`SkillSetSpec`을 실제 로컬 파일 구조로 변환합니다.

초기 권장 출력:

- `SKILL.md`
- `01-clarification.md`
- `02-planning.md`
- `03-validation.md`
- 필요 시 `references/*.md`

권장 구조 예시:

```text
crux-personal-skillset/
  SKILL.md
  00-manifest.json
  01-clarification.md
  02-planning.md
  03-validation.md
  references/
    evidence-summary.md
```

`SKILL.md`는 각 카테고리 문서를 참조하는 얇은 index 파일로 유지합니다. 실질적인 변경은 카테고리 md에 기록하고, `00-manifest.json`에는 버전, 해시, 카테고리 목록, 생성 시각 같은 배포 메타데이터를 둡니다.

초기 단계에서는 자동 생성 대상에서 `scripts/`를 제외하는 것이 안전합니다. 자동화된 텍스트 가이드는 되돌리기 쉽지만, 자동 생성된 스크립트는 로컬 환경에 미치는 영향이 훨씬 큽니다.

### C. Skill Set Deployer

서버는 최신 skill set manifest를 보관하고, CLI는 주기적으로 pull합니다.

신규 동작 제안:

- `crux setup`
  - 초기 skill set bootstrap 수행
- `crux collect`
  - 업로드 후 최신 manifest 확인
- `crux collect --watch`
  - 백그라운드에서 주기적 sync
- `crux skills status`
  - 현재 적용 버전, 마지막 sync, 롤백 가능 버전 표시
- `crux skills rollback --version <n>`
  - 이전 배포본 복구
- `crux skills pause`
  - 자동 갱신 중지
- `crux skills resume`
  - 자동 갱신 재개

로컬 저장 방식:

- 현재 활성 버전: `~/.codex/skills/crux-personal-skillset/`
- 이전 버전 백업: `~/.crux/skillsets/<version>/`
- 로컬 메타데이터: `~/.crux/skillset-state.json`

### D. Shadow Evaluation + Guarded Deployment

자동 업데이트는 생성보다 배포 판정이 더 중요합니다.

권장 배포 단계:

1. 후보 `SkillSetSpec` 생성
2. 기존 버전 대비 semantic diff 계산
3. 충돌/과적합/중복 규칙 검사
4. 최근 세션 기반 shadow score 계산
5. 문서 단위 변경량과 카테고리별 score를 확인
6. 임계치 통과 시 자동 배포
7. 실패 시 배포하지 않고 관측 전용 후보로 유지

즉 자동화의 핵심은 "자동 생성"이 아니라 "자동 배포 판정기"입니다.

### E. 효과 측정 루프

배포 후에는 실제로 개선됐는지 측정해야 합니다.

관측 항목 예시:

- 요구사항 명확화 질문 비율
- 계획 제시 후 구현 시작 비율
- 재작업/수정 요청 빈도
- 잘못된 가정으로 인한 회귀 빈도
- 유사 실패 반복률

가능하면 전체 skill set 지표뿐 아니라 카테고리 단위 지표도 같이 봐야 합니다. 그래야 `clarification`만 교체하고 `validation`은 유지하는 식의 부분 최적화가 가능합니다.

이 데이터는 다음 `SkillSetSpec` 생성 시 다시 입력으로 사용됩니다. 결국 시스템은 한 번 설치되는 skill이 아니라, 성능 데이터로 계속 재작성되는 `personal policy layer`가 됩니다.

---

## 데이터 모델 제안

### 1) `behavior_findings`

- `id`
- `workspace_id`
- `report_id`
- `category`
- `summary`
- `evidence_refs`
- `confidence`
- `status` (`candidate|accepted|rejected|expired`)

### 2) `skill_set_versions`

- `id`
- `workspace_id`
- `version`
- `spec_json`
- `compiled_hash`
- `created_at`
- `deployment_decision` (`deployed|shadow|blocked|rolled_back`)
- `decision_reason`

### 3) `skill_set_deployments`

- `id`
- `workspace_id`
- `version`
- `client_id`
- `deployed_at`
- `rollback_at`
- `sync_status`

### 4) `workspace_skill_settings`

- `workspace_id`
- `mode` (`autopilot|observe_only|frozen`)
- `last_applied_version`
- `last_shadow_version`
- `paused_at`

### 5) 이벤트 로그

- `skillset_candidate_generated`
- `skillset_deployed`
- `skillset_sync_succeeded`
- `skillset_sync_failed`
- `skillset_rolled_back`
- `skillset_auto_blocked`

---

## 품질/안전 가드레일

1. **자동화는 허용하되 실행권한은 제한**
   - 초기 자동 생성 대상은 `SKILL.md`, `references/*.md`로 한정
2. **단일 canonical skill set만 자동 관리**
   - 여러 skill이 난립하면 충돌과 설명 불가능성이 커짐
3. **confidence + impact 이중 임계치**
   - 근거가 충분해도 성능 악화 가능성이 있으면 배포하지 않음
4. **semantic diff 제한**
   - 한 번의 배포에서 너무 많은 규칙을 바꾸지 않음
5. **자동 롤백**
   - 배포 후 지표 악화 시 이전 버전 복구
6. **항상 explainability 제공**
   - 모든 변경은 근거 리포트와 evidence를 역추적 가능해야 함
7. **수동 편집과의 충돌 처리**
   - 사용자가 로컬 skill 파일을 직접 수정한 경우, 자동 동기화 전에 충돌 상태를 기록하고 별도 정책 적용
8. **문서 간 참조 규칙 고정**
   - 카테고리 md 간 include 순서와 naming convention을 고정해 조합 결과가 매번 바뀌지 않게 함

특히 7번은 중요합니다. 자동 시스템이 사용자 파일을 덮어쓰는 순간 제품 신뢰가 깨질 수 있으므로, 자동 관리 대상 skill은 명확히 product-owned path로 분리하는 것이 맞습니다.

---

## 단계별 출시 전략

## Phase 1 (MVP, 2~3주)

- 워크스페이스별 구조화된 multi-file skill bundle 자동 생성
- CLI sync 시 최신 버전 자동 pull
- 대시보드에 `Skill Set Status`와 변경 요약 표시
- 수동 승인 없이 자동 배포, 대신 pause/rollback 제공

성공 기준:

- 활성 워크스페이스 기준 자동 동기화 성공률
- 배포 후 7일 유지율
- 롤백 비율

## Phase 2 (고도화)

- `BehaviorDelta` 누적 모델 도입
- shadow evaluation 기반 자동 배포 판정
- before/after 성능 측정 자동화
- 변경 diff UI 강화

## Phase 3 (자기개선 루프)

- 효과가 낮은 category 문서만 부분 교체
- 사용자/팀 단위 패턴 분리
- 장기적으로는 skill set을 instruction graph 형태로 세분화

---

## 샘플 사용자 여정

1. 사용자는 평소처럼 Codex를 사용한다
2. 서버가 최근 세션에서 "요구사항 불명확 상태에서 바로 구현 시작" 패턴을 반복 탐지한다
3. 다음 리포트 생성 시점에 새로운 `SkillSetSpec v13` 후보가 합성된다
4. shadow evaluation 통과 후 CLI 백그라운드 sync가 `v13`을 자동 배포한다
5. 사용자는 대시보드에서 `Your skill set was updated automatically`와 변경 이유를 확인한다
6. 이후 리포트에서 관련 실패율이 감소했는지 before/after로 확인한다

---

## 구현 체크리스트(엔지니어링)

- 서버 분석: 리포트 결과를 `BehaviorDelta`로 정규화
- 서버 합성기: `BehaviorDelta -> SkillSetSpec`
- 서버 컴파일러: `SkillSetSpec -> categorized md bundle`
- 서버 배포 API: 최신 manifest 조회, 버전 체크, diff 메타데이터 제공
- CLI sync: setup/collect/watch 흐름에 skill set auto-sync 통합
- CLI 제어: `status`, `pause`, `resume`, `rollback`
- 대시보드: `Skill Set Status`, 버전 diff, 근거, 효과 지표
- 관측: 배포 성공률, 롤백률, 성능 개선 여부

이 방향이면 제품은 "리포트를 읽고 사람이 고치는 툴"이 아니라 "사용자의 작업 습관을 자동 운영하는 개인화 계층"으로 진화할 수 있습니다. 핵심은 편집 UI를 더 정교하게 만드는 것이 아니라, 자동 합성/배포/평가 루프를 제품의 기본 동작으로 만드는 것입니다.
