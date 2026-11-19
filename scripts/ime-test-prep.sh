#!/usr/bin/env bash
# ime-test-prep — 切换 Clavis 自研输入法的开关，供 macOS 真机测试用。
#
#   scripts/ime-test-prep.sh on     启用 input_engine（先备份 config.toml）
#   scripts/ime-test-prep.sh off    还原为 enabled = false
#   scripts/ime-test-prep.sh status 打印当前状态与自检摘要
#
# 只改 [input_engine] 段的 enabled 一行，其余配置一字不动。
set -euo pipefail

CFG_DIR="${HOME}/.config/clavis"
CFG="${CFG_DIR}/config.toml"
APP="./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis"

if [[ ! -f "$CFG" ]]; then
  echo "找不到 $CFG —— 先启动一次 Clavis 让它生成默认配置。" >&2
  exit 1
fi

current() {
  awk '/^\[input_engine\]/{f=1;next} /^\[/{f=0} f && /^enabled *=/{
    sub(/^enabled *= */,""); gsub(/ /,""); print; exit
  }' "$CFG"
}

set_enabled() {
  local want="$1"
  local tmp
  tmp="$(mktemp)"
  awk -v want="$want" '
    /^\[input_engine\]/ { insec=1; print; next }
    /^\[/ { insec=0 }
    insec && /^enabled *=/ { print "enabled = " want; done=1; next }
    { print }
    END { if (!done) print "WARN: [input_engine] 段里没有 enabled 行，未修改" > "/dev/stderr" }
  ' "$CFG" > "$tmp"
  if ! cmp -s "$tmp" "$CFG"; then
    cp -p "$CFG" "${CFG}.bak.$(date +%Y%m%d%H%M%S)"
    mv "$tmp" "$CFG"
    echo "已写入 enabled = ${want}（原配置已备份）"
  else
    rm -f "$tmp"
    echo "已是 enabled = ${want}，无需修改"
  fi
}

case "${1:-status}" in
  on)  set_enabled true ;;
  off) set_enabled false ;;
  status|"") : ;;
  *) echo "用法: $0 [on|off|status]" >&2; exit 2 ;;
esac

echo "--- 当前 [input_engine] enabled = $(current)"
if [[ -x "$APP" ]]; then
  echo "--- 自检摘要 ---"
  "$APP" --self-check 2>/dev/null | grep -E "input engine|permissions|input engine probe" || true
else
  echo "（未找到已构建的 app：$APP；先跑 scripts/package-all.sh 或 go tool mygo build -platform darwin/arm64）"
fi

cat <<'EOF'

下一步（on 之后）：
  1. 重启 Clavis
  2. 系统输入法切到英文（ABC）
  3. 按 docs/input-engine-matrix.md §3 逐条测，不通过项记下「应用+按键+实际输出」
  4. 测完执行 scripts/ime-test-prep.sh off 还原
EOF
