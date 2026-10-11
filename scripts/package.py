#!/usr/bin/env python3
"""把 module/ 打包成 Magisk / KernelSU 可刷的 zip。

会自动: 校验结构、把 build/ 里编译好的二进制放进 system/bin/、把文本文件规范成 LF、
重新生成 META-INF（updater-script = "#MAGISK"，update-binary = scripts/module-installer.sh）、
给脚本设 0755 权限。

用法:
    python3 scripts/package.py                              # dist/codex_android-v<版本>.zip
    python3 scripts/package.py -o /tmp/out.zip --bin build
    python3 scripts/package.py --no-bin                     # 不塞二进制（只打源码）
"""
from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import sys
import tempfile
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MODULE_DIR = ROOT / "module" / "codex_android"
INSTALLER = ROOT / "scripts" / "module-installer.sh"
META_DIR = "META-INF/com/google/android"
UPDATER_SCRIPT = "#MAGISK\n"
BINARIES = {"codexweb": "codexweb-arm64", "devctl": "devctl-arm64"}

REQUIRED = ("module.prop", "customize.sh", "service.sh", "gateway.json")
TEXT_SUFFIX = (".sh", ".prop", ".json", ".conf", ".md", ".txt")


def module_version(prop: Path) -> str:
    for line in prop.read_text(encoding="utf-8").splitlines():
        if line.startswith("version="):
            return line.split("=", 1)[1].strip()
    return "0.0.0"


def is_text_file(path: Path) -> bool:
    try:
        raw = path.read_bytes()
    except OSError:
        return False
    return b"\x00" not in raw[:8192]


def normalize_lf(path: Path) -> bool:
    raw = path.read_bytes()
    fixed = raw.replace(b"\r\n", b"\n").replace(b"\r", b"\n")
    if fixed != raw:
        path.write_bytes(fixed)
        return True
    return False


def stage(dest: Path, bin_dir: Path | None) -> list:
    shutil.copytree(MODULE_DIR, dest, dirs_exist_ok=True)
    normalized = []

    for d in ("META-INF", ".git", "__pycache__"):
        p = dest / d
        if p.exists():
            shutil.rmtree(p)

    if bin_dir is not None:
        for dst_name, src_name in BINARIES.items():
            src = bin_dir / src_name
            if not src.is_file():
                print("[package] 缺少 %s，跳过（可先跑 scripts/build.py）" % src,
                      file=sys.stderr)
                continue
            target = dest / "system" / "bin" / dst_name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(src, target)
            print("[package] + system/bin/%s  (%d bytes)" % (dst_name, target.stat().st_size))

    for path in sorted(dest.rglob("*")):
        if path.is_file() and is_text_file(path) and normalize_lf(path):
            normalized.append(str(path.relative_to(dest)).replace(os.sep, "/"))

    meta = dest / META_DIR
    meta.mkdir(parents=True, exist_ok=True)
    (meta / "updater-script").write_bytes(UPDATER_SCRIPT.encode("utf-8"))
    if not INSTALLER.is_file():
        raise SystemExit("[package] 找不到 %s" % INSTALLER)
    shutil.copyfile(INSTALLER, meta / "update-binary")
    return normalized


def make_zip(staging: Path, output: Path) -> list:
    entries = []
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for path in sorted(staging.rglob("*")):
            if not path.is_file():
                continue
            rel = str(path.relative_to(staging)).replace(os.sep, "/")
            if rel.endswith(".sh") or os.path.basename(rel) == "update-binary":
                mode = 0o755
            elif rel.startswith("system/bin/"):
                mode = 0o755
            else:
                mode = 0o644
            info = zipfile.ZipInfo(rel, date_time=(1980, 1, 1, 0, 0, 0))
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            zf.writestr(info, path.read_bytes())
            entries.append((rel, mode))
    return entries


def main():
    version = module_version(MODULE_DIR / "module.prop")
    ap = argparse.ArgumentParser(description="package the Magisk module")
    ap.add_argument("--module", default=str(MODULE_DIR))
    ap.add_argument("--bin", default=str(ROOT / "build"), help="编译产物目录")
    ap.add_argument("--no-bin", action="store_true", help="不用 build/ 里的二进制")
    ap.add_argument("-o", "--output", default=str(ROOT / "dist" / ("codex_android-v%s.zip" % version)))
    args = ap.parse_args()

    mod = Path(args.module)
    if not mod.is_dir():
        raise SystemExit("[package] 模块目录不存在: %s" % mod)
    missing = [f for f in REQUIRED if not (mod / f).is_file()]
    if missing:
        raise SystemExit("[package] 模块缺少必需文件: %s" % ", ".join(missing))

    bin_dir = None if args.no_bin else Path(args.bin)
    if bin_dir is not None and not bin_dir.is_dir():
        print("[package] 提示: %s 不存在，将只打包源码" % bin_dir, file=sys.stderr)
        bin_dir = None

    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix="codex-android-") as tmp:
        staging = Path(tmp) / "module"
        normalized = stage(staging, bin_dir)
        entries = make_zip(staging, output)

    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    print("[package] 输出: %s" % output)
    print("[package] 大小: %d 字节   SHA256: %s" % (output.stat().st_size, digest))
    if normalized:
        print("[package] 已规范为 LF: %s" % ", ".join(normalized))
    print("[package] zip 内容:")
    for rel, mode in entries:
        print("  %o  %s" % (mode, rel))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
