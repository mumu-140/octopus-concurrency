#!/usr/bin/env bash
set -euo pipefail

readonly ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
readonly IMAGE_REPOSITORY="mumu-140/octopus-concurrency"

fail() {
    printf '错误：%s\n' "$1" >&2
    exit 1
}

if [ "$#" -ne 1 ]; then
    fail "用法：$0 v<major>.<minor>.<patch>-mumu.<revision>"
fi

readonly VERSION="$1"
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-mumu\.[0-9]+$ ]]; then
    fail "版本格式无效：$VERSION"
fi

cd "$ROOT_DIR"

git diff --quiet || fail "受跟踪工作树存在未暂存修改"
git diff --cached --quiet || fail "索引中存在未提交修改"

readonly SOURCE_REVISION="$(git rev-parse HEAD)"
readonly SOURCE_TREE="$(git rev-parse 'HEAD^{tree}')"
readonly COMMIT_ID="$(git rev-parse --short=7 HEAD)"
readonly BUILD_TIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
readonly IMAGE="$IMAGE_REPOSITORY:$VERSION"

if git rev-parse --verify --quiet "refs/tags/$VERSION" >/dev/null; then
    readonly TAG_REVISION="$(git rev-list -n 1 "$VERSION")"
    if [ "$TAG_REVISION" != "$SOURCE_REVISION" ]; then
        fail "tag $VERSION 指向 $TAG_REVISION，而不是当前 HEAD"
    fi
fi

printf '正在构建 %s，源码提交 %s，tree %s\n' \
    "$IMAGE" "$SOURCE_REVISION" "$SOURCE_TREE"

DOCKER_BUILDKIT=1 docker build \
    --file "Dockerfile.build" \
    --tag "$IMAGE" \
    --build-arg "GIT_VERSION=$VERSION" \
    --build-arg "COMMIT_ID=$COMMIT_ID" \
    --build-arg "SOURCE_REVISION=$SOURCE_REVISION" \
    --build-arg "SOURCE_TREE=$SOURCE_TREE" \
    --build-arg "BUILD_TIME=$BUILD_TIME" \
    --build-arg "GIT_AUTHOR=mumu-140" \
    --build-arg "SOURCE_URL=https://github.com/mumu-140/octopus-concurrency" \
    "."

docker image inspect "$IMAGE" --format 'id={{.Id}} version={{index .Config.Labels "org.opencontainers.image.version"}} revision={{index .Config.Labels "org.opencontainers.image.revision"}} created={{index .Config.Labels "org.opencontainers.image.created"}}'
