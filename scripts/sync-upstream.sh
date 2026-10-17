#!/usr/bin/env bash
#
# sync-upstream.sh — Đồng bộ code mới từ repo gốc (upstream) về fork này.
#
# Luồng:
#   upstream/master --(fast-forward)--> master --(merge)--> develop
#
# master  : bản phản chiếu upstream, KHÔNG commit tay vào đây.
# develop : branch tích hợp của bạn (upstream + tính năng riêng).
#
# Cách dùng:
#   ./scripts/sync-upstream.sh            # sync master rồi merge vào develop
#   ./scripts/sync-upstream.sh --no-merge # chỉ sync master, không đụng develop
#
set -euo pipefail

UPSTREAM_REMOTE="upstream"
ORIGIN_REMOTE="origin"
BASE_BRANCH="master"      # branch phản chiếu upstream
DEV_BRANCH="develop"      # branch phát triển của bạn

MERGE_DEVELOP=1
if [[ "${1:-}" == "--no-merge" ]]; then
  MERGE_DEVELOP=0
fi

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[!]\033[0m %s\n' "$*"; }
die()   { printf '\033[1;31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# --- Kiểm tra tiền đề -------------------------------------------------------

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "Không ở trong git repo."

if ! git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1; then
  die "Chưa có remote '$UPSTREAM_REMOTE'. Chạy: git remote add $UPSTREAM_REMOTE <url-repo-goc>"
fi

if ! git diff-index --quiet HEAD -- 2>/dev/null; then
  die "Working tree chưa sạch. Hãy commit hoặc stash thay đổi trước khi sync."
fi

START_BRANCH="$(git rev-parse --abbrev-ref HEAD)"

# --- 1. Fetch upstream ------------------------------------------------------

info "Fetch từ '$UPSTREAM_REMOTE'..."
git fetch "$UPSTREAM_REMOTE" --prune

# --- 2. Fast-forward master theo upstream/master ----------------------------

info "Cập nhật '$BASE_BRANCH' theo '$UPSTREAM_REMOTE/$BASE_BRANCH' (fast-forward)..."
git checkout "$BASE_BRANCH"

if ! git merge --ff-only "$UPSTREAM_REMOTE/$BASE_BRANCH"; then
  die "'$BASE_BRANCH' đã phân nhánh khỏi upstream (có commit riêng?). \
master phải luôn sạch. Hãy chuyển commit riêng sang '$DEV_BRANCH'."
fi

info "Đẩy '$BASE_BRANCH' lên '$ORIGIN_REMOTE'..."
git push "$ORIGIN_REMOTE" "$BASE_BRANCH"

# --- 3. Merge master vào develop --------------------------------------------

if [[ "$MERGE_DEVELOP" -eq 1 ]]; then
  info "Merge '$BASE_BRANCH' vào '$DEV_BRANCH'..."
  git checkout "$DEV_BRANCH"

  if git merge --no-edit "$BASE_BRANCH"; then
    info "Đẩy '$DEV_BRANCH' lên '$ORIGIN_REMOTE'..."
    git push "$ORIGIN_REMOTE" "$DEV_BRANCH"
    info "Sync hoàn tất. '$DEV_BRANCH' đã có code mới nhất từ upstream."
  else
    warn "CONFLICT khi merge vào '$DEV_BRANCH'."
    warn "Giải quyết conflict thủ công, sau đó chạy:"
    warn "    git add <files> && git commit"
    warn "    git push $ORIGIN_REMOTE $DEV_BRANCH"
    exit 1
  fi
else
  info "Bỏ qua merge vào '$DEV_BRANCH' (--no-merge)."
  git checkout "$START_BRANCH"
fi
