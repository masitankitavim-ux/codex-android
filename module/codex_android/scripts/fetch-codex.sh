#!/system/bin/sh
# 从 npmmirror 下载 codex 的 linux-arm64 静态二进制并装到 /data/adb/codex/bin/codex
DATA=/data/adb/codex
VER="${CODEX_VERSION:-0.162.0}"
URL="https://registry.npmmirror.com/@openai/codex/-/codex-${VER}-linux-arm64.tgz"
TMP="$DATA/tmp"

mkdir -p "$TMP" "$DATA/bin"
echo "[fetch-codex] $(date) version=$VER"
echo "[fetch-codex] url=$URL"

ok=0
for i in 1 2 3; do
  if curl -fL --connect-timeout 20 --max-time 1200 -o "$TMP/codex.tgz" "$URL"; then ok=1; break; fi
  echo "[fetch-codex] download retry $i"
  sleep 6
done
[ "$ok" = "1" ] || { echo "[fetch-codex] download FAILED"; exit 1; }
[ -s "$TMP/codex.tgz" ] || { echo "[fetch-codex] empty tarball"; exit 1; }

cd "$TMP" || exit 1
rm -rf "$TMP/package"
tar -xzf codex.tgz || { echo "[fetch-codex] untar FAILED"; exit 1; }

SRC="$TMP/package/vendor/aarch64-unknown-linux-musl/bin/codex"
[ -f "$SRC" ] || SRC="$TMP/package/vendor/aarch64-unknown-linux-musl/codex/codex"
if [ -f "$SRC" ]; then
  cp -f "$SRC" "$DATA/bin/codex" && chmod 755 "$DATA/bin/codex"
  echo "[fetch-codex] installed: $(ls -l "$DATA/bin/codex")"
else
  echo "[fetch-codex] binary not found, tarball layout:"
  find "$TMP/package" -maxdepth 4 -type f 2>/dev/null | head -30
  exit 1
fi
rm -rf "$TMP/package" "$TMP/codex.tgz"
echo "[fetch-codex] done"