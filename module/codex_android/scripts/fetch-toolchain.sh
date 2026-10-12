#!/system/bin/sh
# fetch-toolchain.sh —— 按需下载并安装 Python 与 C(zig cc) 开发环境
# 用法：sh fetch-toolchain.sh [python|cc|all] [--force]
DATA=/data/adb/codex
TC=$DATA/toolchain
BIN=$DATA/bin
TMP=$DATA/tmp
mkdir -p "$TC" "$BIN" "$TMP"

# 可覆盖：Python 版本 / 对应 tag / musl 加载器版本 / zig 版本
PY_VER="${PYTHON_VERSION:-3.12.15}"
PY_TAG="${PYTHON_TAG:-20261009}"
MUSL_VER="${MUSL_VERSION:-1.2.5-r11}"
ZIG_VER="${ZIG_VERSION:-0.13.0}"

BB=""
for c in /data/adb/magisk/busybox /sbin/.magisk/busybox /system/xbin/busybox /system/bin/busybox; do
  [ -x "$c" ] && BB="$c" && break
done

log(){ echo "[toolchain] $*"; }

dl() {
  url="$1"; out="$2"; ok=0
  if [ -s "$out" ]; then
    log "reuse existing: $out"
    return 0
  fi
  tmp="$out.part"
  rm -f "$tmp"
  for i in 1 2 3 4 5 6; do
    if curl -fL --connect-timeout 20 --max-time 1800 -o "$tmp" "$url"; then ok=1; break; fi
    log "download retry $i: $url"
    rm -f "$tmp"
    sleep 5
  done
  [ "$ok" = "1" ] || { rm -f "$tmp"; return 1; }
  [ -s "$tmp" ] || { rm -f "$tmp"; return 1; }
  mv -f "$tmp" "$out" || { rm -f "$tmp"; return 1; }
  return 0
}

# python-build-standalone 是动态链接 musl 的（解释器 /lib/ld-musl-aarch64.so.1），
# Android 上没有这个加载器，所以额外下载 musl 并用它直接拉起 python。
install_python() {
  [ -x "$BIN/python3" ] && [ "$1" != "--force" ] && { log "python 已安装"; return 0; }
  log "installing python $PY_VER..."
  pyurl="https://github.com/astral-sh/python-build-standalone/releases/download/$PY_TAG/cpython-$PY_VER+$PY_TAG-aarch64-unknown-linux-musl-install_only_stripped.tar.gz"
  muslurl="https://dl-cdn.alpinelinux.org/alpine/v3.21/main/aarch64/musl-$MUSL_VER.apk"
  dl "$pyurl" "$TMP/python.tar.gz" || { log "python download FAILED"; return 1; }
  dl "$muslurl" "$TMP/musl.apk" || { log "musl loader download FAILED"; return 1; }
  rm -rf "$TC/python"; mkdir -p "$TC/python"
  tar -xzf "$TMP/python.tar.gz" -C "$TC/python" || { log "python untar FAILED"; return 1; }
  rm -rf "$TMP/muslx"; mkdir -p "$TMP/muslx"
  tar -xzf "$TMP/musl.apk" -C "$TMP/muslx" || { log "musl extract FAILED"; return 1; }
  cp -f "$TMP/muslx/lib/ld-musl-aarch64.so.1" "$TC/python/ld-musl-aarch64.so.1" || { log "musl loader copy FAILED"; return 1; }
  chmod 755 "$TC/python/ld-musl-aarch64.so.1"
  # 真实二进制是 python3.x（bin/python3 是指向它的软链），取 -type f 的实体文件
  pybin=$(find "$TC/python" -type f -name 'python3.*' 2>/dev/null | head -1)
  [ -n "$pybin" ] || { log "python binary not found"; find "$TC/python" -maxdepth 3 2>/dev/null | head; return 1; }
  pydir=$(dirname "$pybin"); pybase=$(basename "$pybin")
  cat > "$BIN/python3" <<EOF
#!/system/bin/sh
exec $TC/python/ld-musl-aarch64.so.1 $pydir/$pybase "\$@"
EOF
  chmod 755 "$BIN/python3"
  ln -sf "$BIN/python3" "$BIN/python"
  cat > "$BIN/pip3" <<EOF
#!/system/bin/sh
exec $TC/python/ld-musl-aarch64.so.1 $pydir/$pybase -m pip "\$@"
EOF
  chmod 755 "$BIN/pip3"
  ln -sf "$BIN/pip3" "$BIN/pip"
  rm -f "$TMP/python.tar.gz" "$TMP/musl.apk"; rm -rf "$TMP/muslx"
  "$BIN/python3" -V 2>&1 | head -1
  log "python installed"
}

make_c_wrapper() {
  name="$1"
  cat > "$BIN/$name" <<'EOF'
#!/system/bin/sh
TC=/data/adb/codex/toolchain
export ZIG_GLOBAL_CACHE_DIR=$TC/zig-cache
exec $TC/zig/zig cc -target aarch64-linux-musl "$@"
EOF
  chmod 755 "$BIN/$name"
}

make_cxx_wrapper() {
  name="$1"
  cat > "$BIN/$name" <<'EOF'
#!/system/bin/sh
TC=/data/adb/codex/toolchain
export ZIG_GLOBAL_CACHE_DIR=$TC/zig-cache
exec $TC/zig/zig c++ -target aarch64-linux-musl "$@"
EOF
  chmod 755 "$BIN/$name"
}

install_cc() {
  [ -x "$BIN/cc" ] && [ -x "$TC/zig/zig" ] && [ "$1" != "--force" ] && { log "cc(zig) 已安装"; return 0; }
  log "installing zig cc (C/C++ compiler) $ZIG_VER..."
  zurl="https://ziglang.org/download/$ZIG_VER/zig-linux-aarch64-$ZIG_VER.tar.xz"
  dl "$zurl" "$TMP/zig.tar.xz" || { log "zig download FAILED"; return 1; }
  rm -rf "$TMP/zx" "$TC/zig"; mkdir -p "$TMP/zx" "$TC/zig"
  if [ -n "$BB" ]; then "$BB" unxz -c "$TMP/zig.tar.xz" | "$BB" tar -x -C "$TMP/zx"; else tar -xJf "$TMP/zig.tar.xz" -C "$TMP/zx"; fi \
    || { log "zig extract FAILED"; return 1; }
  zroot=$(find "$TMP/zx" -maxdepth 2 -type d -name 'zig-linux-aarch64-*' 2>/dev/null | head -1)
  [ -n "$zroot" ] || zroot=$(find "$TMP/zx" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -1)
  [ -n "$zroot" ] || { log "zig root not found"; find "$TMP/zx" -maxdepth 2 2>/dev/null | head; return 1; }
  mv "$zroot"/* "$TC/zig"/ 2>/dev/null || { log "zig move FAILED"; return 1; }
  [ -x "$TC/zig/zig" ] || { log "zig binary not found"; find "$TC/zig" -maxdepth 2 -type f 2>/dev/null | head; return 1; }
  chmod 755 "$TC/zig/zig"
  ln -sf "$TC/zig/zig" "$BIN/zig"
  make_c_wrapper cc
  make_c_wrapper gcc
  make_c_wrapper clang
  make_cxx_wrapper c++
  make_cxx_wrapper g++
  rm -f "$TMP/zig.tar.xz"; rm -rf "$TMP/zx"
  "$BIN/cc" --version 2>&1 | head -3
  cat > "$TMP/hello.c" <<'CEOF'
#include <stdio.h>
int main(){ printf("hi from zig cc\n"); return 0; }
CEOF
  if ( cd "$TMP" && "$BIN/cc" hello.c -o hello && ./hello ); then
    log "hello world self-check OK"
  else
    log "warning: hello.c self-check failed"
  fi
  rm -f "$TMP/hello.c" "$TMP/hello"
  log "cc(zig) installed"
}

case "$1" in
  python) install_python "$2" ;;
  cc)     install_cc "$2" ;;
  all|"") install_python "$2"; install_cc "$2" ;;
  *) echo "usage: fetch-toolchain.sh [python|cc|all] [--force]"; exit 1 ;;
esac
echo "[toolchain] done"