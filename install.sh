#!/usr/bin/env bash
# Install the doubao plugin into a CLIProxyAPI deployment.
#
#   ./install.sh [target-dir]
#
# target-dir defaults to the current directory and is expected to be a CPA
# installation, i.e. to contain config.yaml.
set -euo pipefail

TARGET="${1:-.}"
PLUGIN_NAME="doubao"

say()  { printf '%s\n' "$*"; }
fail() { printf '错误: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------

command -v go >/dev/null 2>&1 || fail "未找到 go，请先安装 Go 1.26+"

# The plugin is a c-shared library, so a C toolchain is required. CPA itself must
# also be cgo-enabled or it cannot load any plugin; that is worth stating up
# front because the failure mode is an unhelpful "plugin loading requires cgo".
command -v cc >/dev/null 2>&1 || command -v gcc >/dev/null 2>&1 \
  || fail "未找到 C 编译器（gcc/cc），c-shared 插件需要 CGO"

[ -f "$TARGET/config.yaml" ] \
  || fail "$TARGET 下没有 config.yaml，请把 CPA 安装目录作为参数传入"

PLUGIN_DIR="$TARGET/plugins"
mkdir -p "$PLUGIN_DIR"

say "==> 构建 $PLUGIN_NAME.so"
CGO_ENABLED=1 go build -buildmode=c-shared -o "$PLUGIN_DIR/$PLUGIN_NAME.so" .
# The build emits a header alongside the library; CPA does not use it and it only
# confuses later readers of the plugins directory.
rm -f "$PLUGIN_DIR/$PLUGIN_NAME.h"
[ -f "$PLUGIN_DIR/$PLUGIN_NAME.so" ] || fail "构建失败：没有产出 $PLUGIN_NAME.so"

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

say "==> 检查 config.yaml"

if grep -qE '^[[:space:]]*plugins:' "$TARGET/config.yaml"; then
  say "    已存在 plugins 配置段，请手工确认下列内容："
  cat <<EOF

    plugins:
      enabled: true
      dir: "plugins"
      configs:
        $PLUGIN_NAME:
          enabled: true
          priority: 20
          realm_default: doubao    # doubao=国内版豆包, dola=国际版
          expose_models: true

EOF
else
  say "    追加 plugins 配置段"
  cat >>"$TARGET/config.yaml" <<EOF

# --- $PLUGIN_NAME plugin ---
plugins:
  enabled: true
  dir: "plugins"
  configs:
    $PLUGIN_NAME:
      enabled: true
      priority: 20
      realm_default: doubao    # doubao=国内版豆包, dola=国际版
      expose_models: true
EOF
fi

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------

cat <<EOF

==> 完成

已安装： $PLUGIN_DIR/$PLUGIN_NAME.so

下一步：
  1. 重启 CPA
  2. 打开 CPA 的插件面板，选择「豆包 / Dola」
  3. 按面板提示粘贴登录后的完整 Cookie

验证：
  curl http://localhost:PORT/v1/models | grep $PLUGIN_NAME

注意：CPA 必须是用 CGO_ENABLED=1 编译的版本，否则无法加载任何插件。
EOF
