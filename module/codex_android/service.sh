#!/system/bin/sh
MODDIR=${0%/*}
DATA=/data/adb/codex
LOG=$DATA/codexweb.log
PORT=8787

mkdir -p "$DATA/bin" "$DATA/web/threads" "$DATA/workspace" "$DATA/tmp"
[ -f "$DATA/gateway.json" ] || cp -f "$MODDIR/gateway.json" "$DATA/gateway.json"
chmod 700 "$DATA" 2>/dev/null

export PATH=/system/bin:/system/xbin:/data/adb/magisk:$PATH
export HOME="$DATA"
export CODEX_HOME="$DATA"
export TMPDIR="$DATA/tmp"
export SHELL=/system/bin/sh

# 等开机完成
i=0
while [ "$(getprop sys.boot_completed)" != "1" ] && [ $i -lt 90 ]; do sleep 2; i=$((i+1)); done
sleep 3

# 1) 刷新 /system/etc/resolv.conf（musl / Go 静态二进制只认这个文件）
sh "$MODDIR/scripts/resolv.sh" 2>/dev/null

# 2) CA 证书：Go 静态二进制在安卓上不会自动读系统证书库，需要显式指定
if [ ! -s "$DATA/ca-bundle.crt" ]; then
  cat /system/etc/security/cacerts/* > "$DATA/ca-bundle.crt" 2>/dev/null
fi
export SSL_CERT_FILE="$DATA/ca-bundle.crt"

echo "[codex] boot $(date) moddir=$MODDIR" >> "$LOG"

# 3) codex 二进制缺失则后台下载
if [ ! -x "$DATA/bin/codex" ]; then
  ( sh "$MODDIR/scripts/fetch-codex.sh" >> "$LOG" 2>&1 ) &
fi

# 4) 启动网关（常驻）
if ! pgrep -f codexweb >/dev/null 2>&1; then
  ( "$MODDIR/system/bin/codexweb" -home "$DATA" -codex "$DATA/bin/codex" -addr "0.0.0.0:$PORT" >> "$LOG" 2>&1 ) &
fi

exit 0