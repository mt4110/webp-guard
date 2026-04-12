#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(CDPATH='' cd -- "${SCRIPT_DIR}/.." && pwd)"

pick_directory() {
  local selected=""

  if command -v zenity >/dev/null 2>&1 && { [[ -n "${DISPLAY:-}" ]] || [[ -n "${WAYLAND_DISPLAY:-}" ]]; }; then
    selected="$(zenity --file-selection --directory --title='Choose an image directory' 2>/dev/null || true)"
  fi

  if [[ -z "${selected}" ]]; then
    printf 'Directory to process: ' >&2
    read -r selected
  fi

  printf '%s' "${selected}"
}

resolve_target_dir() {
  local dir="$1"

  if [[ "${dir}" != /* ]]; then
    dir="${REPO_ROOT}/${dir}"
  fi

  if [[ ! -d "${dir}" ]]; then
    return 1
  fi

  (
    cd "${dir}"
    pwd -P
  )
}

run_cli() {
  local dir="$1"
  shift

  cd "${REPO_ROOT}"

  if command -v go >/dev/null 2>&1; then
    exec go run . bulk --dir "${dir}" "$@"
  fi

  if [[ -x "${REPO_ROOT}/webp-guard" ]]; then
    exec "${REPO_ROOT}/webp-guard" bulk --dir "${dir}" "$@"
  fi

  if command -v webp-guard >/dev/null 2>&1; then
    exec webp-guard bulk --dir "${dir}" "$@"
  fi

  printf 'webp-guard requires either Go (for go run) or a built webp-guard binary.\n' >&2
  exit 1
}

TARGET_DIR="${1:-}"
if [[ -n "${TARGET_DIR}" ]]; then
  shift
else
  TARGET_DIR="$(pick_directory)"
fi

if [[ -z "${TARGET_DIR}" ]]; then
  printf 'No directory selected.\n' >&2
  exit 1
fi

if ! TARGET_DIR="$(resolve_target_dir "${TARGET_DIR}")"; then
  printf 'Directory not found: %s\n' "${TARGET_DIR}" >&2
  exit 1
fi

run_cli "${TARGET_DIR}" "$@"
