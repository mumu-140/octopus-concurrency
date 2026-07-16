#!/usr/bin/env bash
set -euo pipefail

readonly ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"

git -C "$ROOT_DIR" rev-parse --git-dir >/dev/null
test -x "$ROOT_DIR/.githooks/pre-push"
git -C "$ROOT_DIR" config core.hooksPath .githooks

actual="$(git -C "$ROOT_DIR" config --get core.hooksPath)"
if [ "$actual" != ".githooks" ]; then
    printf 'failed to install repository hooks: core.hooksPath=%s\n' "$actual" >&2
    exit 1
fi

printf 'repository hooks installed: %s/.githooks\n' "$ROOT_DIR"
