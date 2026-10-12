#!/system/bin/sh
# 生成 /system/etc/resolv.conf。
# 安卓原生没有这个文件（bionic 走 netd），而 musl 静态二进制（codex）和 Go 的
# 纯 Go 解析器都只读 /etc/resolv.conf，缺文件会直接 DNS 失败。
# 本脚本写在模块的 system/etc/resolv.conf 上，Magisk magic mount 会把它挂到 /system/etc。
MODDIR=${0%/*}/..
OUT="$MODDIR/system/etc/resolv.conf"
TMP="$OUT.tmp"

: > "$TMP"
for p in net.dns1 net.dns2 net.dns3 net.dns4; do
  v=$(getprop $p 2>/dev/null)
  case "$v" in
    *:*) continue ;;
    [0-9]*.[0-9]*.[0-9]*.[0-9]*) echo "nameserver $v" >> "$TMP" ;;
  esac
done
echo "nameserver 223.5.5.5" >> "$TMP"
echo "nameserver 119.29.29.29" >> "$TMP"
sort -u "$TMP" > "$OUT" 2>/dev/null || cp -f "$TMP" "$OUT"
rm -f "$TMP"