#!/usr/bin/env bash
set -Eeuo pipefail

readonly ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
readonly APP_NAME="octopus"
readonly OUTPUT_DIR="$ROOT_DIR/build"
readonly WEB_DIR="$ROOT_DIR/web"
readonly STATIC_DIR="$ROOT_DIR/static/out"
readonly AUTHOR="mumu-140"
readonly UPDATE_PRICE_DATA="${UPDATE_PRICE_DATA:-0}"

cd "$ROOT_DIR"

readonly COMMIT_ID="$(git rev-parse --short=7 HEAD)"
readonly EXACT_TAG="$(git describe --tags --exact-match HEAD 2>/dev/null || true)"
readonly VERSION="${GIT_VERSION:-${EXACT_TAG:-dev-$COMMIT_ID}}"
readonly BUILD_TIME="${BUILD_TIME:-$(date -u +'%Y-%m-%dT%H:%M:%SZ')}"
readonly LDFLAGS="-X 'github.com/bestruirui/octopus/internal/conf.Version=$VERSION' \
  -X 'github.com/bestruirui/octopus/internal/conf.BuildTime=$BUILD_TIME' \
  -X 'github.com/bestruirui/octopus/internal/conf.Author=$AUTHOR' \
  -X 'github.com/bestruirui/octopus/internal/conf.Commit=$COMMIT_ID' \
  -s -w"

fail() {
    printf 'Error: %s\n' "$1" >&2
    exit 1
}

on_error() {
    local exit_code="$1"
    local line_number="$2"
    printf 'Build failed at line %s with exit code %s.\n' \
        "$line_number" "$exit_code" >&2
    exit "$exit_code"
}

trap 'on_error "$?" "$LINENO"' ERR

usage() {
    cat <<'EOF'
Usage:
  scripts/build.sh build <os> <arch>
  scripts/build.sh release

Supported OS: linux, windows, darwin, android
Supported architectures: x86_64, arm64, armv7, x86

Set UPDATE_PRICE_DATA=1 only when intentionally refreshing price presets.
EOF
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

prepare_environment() {
    if [ "$UPDATE_PRICE_DATA" != "0" ] && [ "$UPDATE_PRICE_DATA" != "1" ]; then
        fail "UPDATE_PRICE_DATA must be 0 or 1"
    fi

    require_command git
    require_command go
    require_command node
    require_command pnpm

    if [ "$UPDATE_PRICE_DATA" = "1" ]; then
        require_command python3
    fi

    mkdir -p "$OUTPUT_DIR/bin" "$OUTPUT_DIR/docker" "$OUTPUT_DIR/archives"
    go mod download
    go mod verify
}

build_frontend() {
    (
        cd "$WEB_DIR"
        pnpm install --frozen-lockfile
        NEXT_PUBLIC_APP_VERSION="$VERSION" pnpm run build
    )

    rm -rf -- "$STATIC_DIR"
    mkdir -p "$(dirname "$STATIC_DIR")"
    mv "$WEB_DIR/out" "$STATIC_DIR"
}

refresh_price_data() {
    if [ "$UPDATE_PRICE_DATA" = "1" ]; then
        python3 "$ROOT_DIR/scripts/updatePrice.py"
    else
        printf 'Using committed price presets.\n'
    fi
}

validate_target() {
    case "$1" in
        linux | windows | darwin | android) ;;
        *) fail "unsupported OS: $1" ;;
    esac

    case "$2" in
        x86_64 | arm64 | armv7 | x86) ;;
        *) fail "unsupported architecture: $2" ;;
    esac
}

binary_path() {
    local os="$1"
    local arch="$2"
    local extension=""
    if [ "$os" = "windows" ]; then
        extension=".exe"
    fi
    printf '%s/bin/%s-%s-%s%s' "$OUTPUT_DIR" "$APP_NAME" "$os" "$arch" "$extension"
}

build_target() {
    local os="$1"
    local arch="$2"
    local goarch
    local -a build_env=("GOOS=$os" "CGO_ENABLED=0")

    validate_target "$os" "$arch"
    case "$arch" in
        x86_64) goarch="amd64" ;;
        arm64) goarch="arm64" ;;
        armv7)
            goarch="arm"
            build_env+=("GOARM=7")
            ;;
        x86) goarch="386" ;;
    esac
    build_env+=("GOARCH=$goarch")

    printf 'Building %s %s for %s/%s.\n' "$APP_NAME" "$VERSION" "$os" "$arch"
    env "${build_env[@]}" go build \
        -buildvcs=false \
        -o "$(binary_path "$os" "$arch")" \
        -ldflags "$LDFLAGS" \
        -tags jsoniter \
        ./
}

prepare_docker_binary() {
    local arch="$1"
    local platform_arch

    case "$arch" in
        x86_64) platform_arch="amd64" ;;
        x86) platform_arch="386" ;;
        armv7) platform_arch="arm/v7" ;;
        arm64) platform_arch="arm64" ;;
    esac

    mkdir -p "$OUTPUT_DIR/docker/linux/$platform_arch"
    cp "$(binary_path linux "$arch")" \
        "$OUTPUT_DIR/docker/linux/$platform_arch/$APP_NAME"
}

package_target() {
    local os="$1"
    local arch="$2"
    local source_file
    local packaged_binary="$APP_NAME"
    local archive_name="$APP_NAME-$os-$arch.zip"

    source_file="$(binary_path "$os" "$arch")"
    if [ "$os" = "windows" ]; then
        packaged_binary="$APP_NAME.exe"
    fi

    cp "$source_file" "$OUTPUT_DIR/archives/$packaged_binary"
    (
        cd "$OUTPUT_DIR/archives"
        zip -q "$archive_name" "$packaged_binary" README.md LICENSE
    )
    rm -f -- "$OUTPUT_DIR/archives/$packaged_binary"
}

write_checksums() {
    (
        cd "$OUTPUT_DIR"
        find bin archives -maxdepth 1 -type f -print0 \
            | sort -z \
            | xargs -0 sha256sum >.SHA256SUMS.tmp
        mv .SHA256SUMS.tmp archives/SHA256SUMS
    )
}

build_release() {
    local -a targets=(
        "linux x86_64"
        "linux arm64"
        "linux armv7"
        "linux x86"
        "windows x86_64"
        "windows x86"
        "darwin arm64"
        "darwin x86_64"
    )

    require_command zip
    require_command sha256sum
    rm -rf -- "$OUTPUT_DIR/bin" "$OUTPUT_DIR/docker" "$OUTPUT_DIR/archives"
    mkdir -p "$OUTPUT_DIR/bin" "$OUTPUT_DIR/docker" "$OUTPUT_DIR/archives"
    cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$OUTPUT_DIR/archives/"

    local target
    local target_os
    local target_arch
    for target in "${targets[@]}"; do
        read -r target_os target_arch <<<"$target"
        build_target "$target_os" "$target_arch"
        package_target "$target_os" "$target_arch"
    done

    local arch
    for arch in x86_64 arm64 armv7 x86; do
        prepare_docker_binary "$arch"
    done

    rm -f -- "$OUTPUT_DIR/archives/README.md" "$OUTPUT_DIR/archives/LICENSE"
    write_checksums
    printf 'Release artifacts are ready in %s.\n' "$OUTPUT_DIR"
}

main() {
    case "${1:-}" in
        build)
            [ "$#" -eq 3 ] || fail "build requires <os> and <arch>"
            validate_target "$2" "$3"
            prepare_environment
            build_frontend
            refresh_price_data
            build_target "$2" "$3"
            ;;
        release)
            [ "$#" -eq 1 ] || fail "release does not accept extra arguments"
            prepare_environment
            build_frontend
            refresh_price_data
            build_release
            ;;
        help | -h | --help)
            usage
            ;;
        *)
            usage >&2
            exit 1
            ;;
    esac
}

main "$@"
