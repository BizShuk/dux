#!/usr/bin/env bash
# run:setup — 冪等 (idempotent) 的前置作業，不啟動任何服務。
# 設定的唯一位置是 ~/.config/dux，repo 內的 tmp/config 只是指過去的 symlink。

set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
config_root="${XDG_CONFIG_HOME:-${HOME}/.config}/dux"

mkdir -p "$config_root/data" "$config_root/logs" "$project_root/tmp"
ln -sfn "$config_root" "$project_root/tmp/config"

printf 'dux config link: %s -> %s\n' "$project_root/tmp/config" "$config_root"
