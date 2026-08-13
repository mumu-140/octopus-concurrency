#!/usr/bin/env bash
set -euo pipefail

readonly ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly STATE_FILE="$ROOT_DIR/deploy/fwq57ys/production-state.json"

# 公开仓库只含占位路径（/opt/octopus-*、203.0.113.10）。
# 生产机通过 gitignored 的 deploy/fwq57ys/.deploy-local.env 注入真实部署路径，
# 使 --live 门禁在真实环境仍可用；CI/公开环境无此文件时使用占位值。
DEPLOY_LOCAL_ENV="$ROOT_DIR/deploy/fwq57ys/.deploy-local.env"
if [ -f "$DEPLOY_LOCAL_ENV" ]; then
    # shellcheck disable=SC1090
    . "$DEPLOY_LOCAL_ENV"
fi
OCTOPUS_DEPLOY_ROOT="${OCTOPUS_DEPLOY_ROOT:-/opt/octopus}"
OCTOPUS_SRC_DIR="${OCTOPUS_SRC_DIR:-/opt/octopus-mumu}"
OCTOPUS_LAN_IP="${OCTOPUS_LAN_IP:-203.0.113.10}"

# 把 state 中的占位路径还原为真实路径（生产机覆盖；公开环境原样返回）
real_path() {
    local p="$1"
    p="${p//\/opt\/octopus-mumu/$OCTOPUS_SRC_DIR}"
    p="${p//\/opt\/octopus/$OCTOPUS_DEPLOY_ROOT}"
    printf '%s' "$p"
}
real_url() { printf '%s' "${1//203.0.113.10/$OCTOPUS_LAN_IP}"; }

fail() {
    printf 'governance check failed: %s\n' "$1" >&2
    exit 1
}

pass() {
    printf 'governance check passed: %s\n' "$1"
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

read_state() {
    jq -er "$1" "$STATE_FILE"
}

check_required_files() {
    local file
    for file in \
        .githooks/pre-push \
        .github/CODEOWNERS \
        .github/pull_request_template.md \
        .github/workflows/ci.yml \
        AGENTS.md \
        docs/octopus-development-governance.md \
        docs/octopus-production.md \
        deploy/fwq57ys/compose.yaml \
        deploy/fwq57ys/production-state.json \
        Dockerfile.build \
        scripts/build-production-image.sh \
        web/pnpm-workspace.yaml; do
        [ -f "$ROOT_DIR/$file" ] || fail "missing required file: $file"
    done
}

require_document_entries() {
    local file="$1"
    shift
    local entry
    for entry in "$@"; do
        grep -Fqx "$entry" "$file" \
            || fail "missing document contract in $file: $entry"
    done
}

require_document_text() {
    local file="$1"
    shift
    local required_text
    for required_text in "$@"; do
        grep -Fq "$required_text" "$file" \
            || fail "missing required guidance in $file: $required_text"
    done
}

check_manual_contracts() {
    local development="$ROOT_DIR/docs/octopus-development-governance.md"
    local production="$ROOT_DIR/docs/octopus-production.md"
    local index
    require_document_entries "$development" \
        "## 修改路由" "## 按改动类型验证" "## 价格与费用契约" \
        "## 明确禁止" "## 已知缺陷与历史坑" \
        "## 停止条件" "## 交付证据"
    require_document_entries "$production" \
        "## 职责与边界" "## 镜像与源码选择" "## 候选验证" \
        "## 数据备份" "## 生产切换" "## 验证与回滚" \
        "## 已知事故与处理" "## 停止条件" "## 交付证据"
    require_document_text "$development" \
        "/opt/octopus-mumu/" "stats_leaderboard" \
        "item_reference" "web/pnpm-workspace.yaml" "UPDATE_PRICE_DATA=1" \
        "actual_model_name" "request_model_name"
    require_document_text "$production" \
        "octopus-candidate-<version>" "read:packages" "pull_policy: never" \
        "SELECT COUNT(*) FROM sqlite_schema;" "30 分钟" "自动回滚"
    for index in AGENTS.md CLAUDE.md; do
        require_document_text "$ROOT_DIR/$index" \
            "docs/octopus-development-governance.md" \
            "docs/octopus-production.md"
    done
    if git -C "$ROOT_DIR" grep -n -E \
        "docs/(development-governance|production)\.md" -- . >/dev/null; then
        fail "obsolete generic Octopus manual path detected"
    fi
}

check_versions() {
    local version
    local go_version
    local web_version
    local compose_image
    local expected_image

    version="$(read_state '.production.release.version')"
    go_version="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT_DIR/internal/conf/version.go")"
    web_version="$(jq -er '.version' "$ROOT_DIR/web/package.json")"
    compose_image="$(sed -n 's/^[[:space:]]*image:[[:space:]]*//p' "$ROOT_DIR/deploy/fwq57ys/compose.yaml" | head -n 1)"
    expected_image="$(read_state '.production.container.image')"

    [ "$go_version" = "$version" ] || fail "Go version $go_version does not match $version"
    [ "$web_version" = "${version#v}" ] || fail "web version $web_version does not match $version"
    grep -Fq "|| '$version'" "$ROOT_DIR/web/src/lib/info.ts" \
        || fail "frontend fallback version does not match $version"
    [ "$compose_image" = "$expected_image" ] \
        || fail "managed compose image $compose_image does not match $expected_image"
}

check_git_truth() {
    local release_tag
    local source_commit
    local tag_commit
    local minimum_commit

    release_tag="$(read_state '.production.release.tag')"
    source_commit="$(read_state '.production.release.sourceCommit')"
    minimum_commit="$(read_state '.repository.minimumNormalizedCommit')"
    tag_commit="$(git -C "$ROOT_DIR" rev-parse "${release_tag}^{}")"

    [ "$tag_commit" = "$source_commit" ] \
        || fail "release tag $release_tag resolves to $tag_commit, expected $source_commit"
    git -C "$ROOT_DIR" merge-base --is-ancestor "$minimum_commit" HEAD \
        || fail "HEAD does not include normalized baseline $minimum_commit"
}

check_sensitive_files() {
    local tracked
    tracked="$(git -C "$ROOT_DIR" ls-files)"

    if printf '%s\n' "$tracked" \
        | grep -E '(^|/)(data|data-dev)/|(^|/)[^/]*\.db(-wal|-shm)?$' >/dev/null; then
        fail "tracked runtime data or database file detected"
    fi
    if printf '%s\n' "$tracked" \
        | grep -E '(^|/)\.env($|\.)' \
        | grep -vE '\.env\.example$' >/dev/null; then
        fail "tracked environment credential file detected"
    fi
}

check_workflows() {
    local branch_trigger

    if grep -RInE 'push[[:space:]]+.*--force|origin/dev|refs/heads/master' \
        "$ROOT_DIR/.github" >/dev/null; then
        fail "obsolete or history-rewriting workflow detected"
    fi
    grep -Fq '  push:' "$ROOT_DIR/.github/workflows/ci.yml" \
        || fail "CI push trigger missing"
    for branch_trigger in \
        '      - main' \
        '      - "codex/**"' \
        '      - "feat/**"' \
        '      - "fix/**"' \
        '      - "chore/**"'; do
        grep -Fq "$branch_trigger" "$ROOT_DIR/.github/workflows/ci.yml" \
            || fail "CI branch trigger missing: $branch_trigger"
    done
    grep -Fq 'run: bash scripts/check-governance.sh --repo' \
        "$ROOT_DIR/.github/workflows/ci.yml" \
        || fail "CI governance job missing"
}

check_shell() {
    bash -n "$ROOT_DIR/scripts/build.sh"
    bash -n "$ROOT_DIR/scripts/build-production-image.sh"
    bash -n "$ROOT_DIR/scripts/check-governance.sh"
    bash -n "$ROOT_DIR/scripts/install-git-hooks.sh"
    bash -n "$ROOT_DIR/.githooks/pre-push"
}

check_repository() {
    require_command git
    require_command jq
    jq -e '.schemaVersion == 1' "$STATE_FILE" >/dev/null \
        || fail "invalid production state schema"
    check_required_files
    check_manual_contracts
    check_versions
    check_git_truth
    check_sensitive_files
    check_workflows
    check_shell
    pass "repository rules, versions, tags, workflows and sensitive-file boundary"
}

assert_equal() {
    local label="$1"
    local actual="$2"
    local expected="$3"
    [ "$actual" = "$expected" ] \
        || fail "$label is $actual, expected $expected"
}

check_live_container() {
    local container_name
    local inspect
    local image_inspect
    local source_commit

    container_name="$(read_state '.production.container.name')"
    inspect="$(docker inspect "$container_name")"
    image_inspect="$(docker image inspect "$(read_state '.production.container.imageId')")"
    source_commit="$(read_state '.production.release.sourceCommit')"

    assert_equal "container id" \
        "$(jq -er '.[0].Id' <<<"$inspect")" \
        "$(read_state '.production.container.id')"
    assert_equal "container image reference" \
        "$(jq -er '.[0].Config.Image' <<<"$inspect")" \
        "$(read_state '.production.container.image')"
    assert_equal "container image id" \
        "$(jq -er '.[0].Image' <<<"$inspect")" \
        "$(read_state '.production.container.imageId')"
    assert_equal "container startedAt" \
        "$(jq -er '.[0].State.StartedAt' <<<"$inspect")" \
        "$(read_state '.production.container.startedAt')"
    assert_equal "container network" \
        "$(jq -er '.[0].HostConfig.NetworkMode' <<<"$inspect")" \
        "$(read_state '.production.container.networkMode')"
    assert_equal "container restart count" \
        "$(jq -er '.[0].RestartCount | tostring' <<<"$inspect")" \
        "$(read_state '.production.container.restartCount | tostring')"
    assert_equal "image version label" \
        "$(jq -er '.[0].Config.Labels.version' <<<"$image_inspect")" \
        "$(read_state '.production.release.version')"
    assert_equal "image source commit label" \
        "$(jq -er '.[0].Config.Labels.commit' <<<"$image_inspect")" \
        "${source_commit:0:7}"
    jq -e \
        --arg source "$(real_path "$(read_state '.production.container.dataMount.source')")" \
        --arg destination "$(read_state '.production.container.dataMount.destination')" \
        --argjson read_write "$(read_state '.production.container.dataMount.readWrite')" \
        '.[0].State.Running == true and (.[0].Mounts | any(.Source == $source and .Destination == $destination and .RW == $read_write))' \
        <<<"$inspect" >/dev/null \
        || fail "container is not running with the expected data mount mode"
}

check_live() {
    local canonical_path
    local compose_replica
    local rollback_snapshot
    local url
    local code

    require_command docker
    require_command curl
    canonical_path="$(real_path "$(read_state '.repository.canonicalPath')")"
    compose_replica="$(real_path "$(read_state '.production.composeReplica')")"
    rollback_snapshot="$(real_path "$(read_state '.production.rollbackSnapshot')")"
    [ "$ROOT_DIR" = "$canonical_path" ] \
        || fail "live check must run from canonical repository $canonical_path"
    cmp -s "$ROOT_DIR/$(read_state '.production.managedCompose')" "$compose_replica" \
        || fail "production compose replica drifted from the managed compose"
    [ -d "$rollback_snapshot" ] \
        || fail "rollback snapshot is missing: $rollback_snapshot"
    check_live_container

    while IFS= read -r url; do
        url="$(real_url "$url")"
    code="$(curl --connect-timeout 5 --max-time 15 -fsS -o /dev/null -w '%{http_code}' "$url")"
        [ "$code" = "200" ] || fail "$url returned HTTP $code"
    done < <(jq -er '.production.httpChecks[]' "$STATE_FILE")
    pass "live container, compose, mount and HTTP baseline"
}

case "${1:---repo}" in
    --repo)
        check_repository
        ;;
    --live)
        check_repository
        check_live
        ;;
    *)
        fail "usage: scripts/check-governance.sh [--repo|--live]"
        ;;
esac
