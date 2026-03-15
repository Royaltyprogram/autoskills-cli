#!/bin/bash
# Cursor 완전 삭제 후 재설치 스크립트
# 실행 전 반드시 Cursor를 완전히 종료하세요.

set -e

echo "=== Cursor 완전 삭제 스크립트 ==="
echo ""
echo "다음 항목들이 삭제됩니다:"
echo "  1. /Applications/Cursor.app"
echo "  2. ~/Library/Application Support/Cursor"
echo "  3. ~/.cursor (설정, 확장, 프로젝트 메타데이터)"
echo "  4. ~/Library/Caches/Cursor"
echo "  5. ~/Library/Preferences/com.todesktop.* (Cursor 관련)"
echo "  6. ~/Library/Saved Application State/com.todesktop.*"
echo ""
read -p "계속하시겠습니까? (y/N): " confirm
[[ "$confirm" != "y" && "$confirm" != "Y" ]] && echo "취소됨" && exit 0

# Cursor 프로세스 종료
echo "Cursor 프로세스 종료 중..."
pkill -x Cursor 2>/dev/null || true
sleep 2

# 앱 삭제
echo "1. Cursor.app 삭제..."
rm -rf /Applications/Cursor.app

# Application Support 삭제 (캐시, 세션, 구버전 데이터 포함)
echo "2. Application Support 삭제..."
rm -rf "$HOME/Library/Application Support/Cursor"

# 사용자 설정 삭제 (확장, skills, 프로젝트 메타 등)
echo "3. ~/.cursor 삭제..."
rm -rf "$HOME/.cursor"

# 캐시 삭제
echo "4. Caches 삭제..."
rm -rf "$HOME/Library/Caches/Cursor"
rm -rf "$HOME/Library/Caches/cursor-compile-cache"

# Preferences 삭제
echo "5. Preferences 삭제..."
rm -f "$HOME/Library/Preferences/com.todesktop."* 2>/dev/null || true

# Saved Application State 삭제
echo "6. Saved Application State 삭제..."
rm -rf "$HOME/Library/Saved Application State/com.todesktop."* 2>/dev/null || true

echo ""
echo "=== 삭제 완료 ==="
echo ""
echo "재설치 방법:"
echo "  1. https://cursor.com 에서 최신 버전 다운로드"
echo "  2. 다운로드한 .dmg 파일 열기"
echo "  3. Cursor.app을 Applications 폴더로 드래그"
echo ""
