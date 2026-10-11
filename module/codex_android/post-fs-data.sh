#!/system/bin/sh
MODDIR=${0%/*}
DATA=/data/adb/codex

mkdir -p "$DATA/bin" "$DATA/web/threads" "$DATA/workspace" "$DATA/tmp"
chmod 700 "$DATA" 2>/dev/null
[ -f "$DATA/gateway.json" ] || cp -f "$MODDIR/gateway.json" "$DATA/gateway.json" 2>/dev/null
[ -f "$DATA/gateway.json" ] && chmod 600 "$DATA/gateway.json"

# 早期先落一份 DNS；service.sh 会按当前网络再刷新一次
sh "$MODDIR/scripts/resolv.sh" 2>/dev/null