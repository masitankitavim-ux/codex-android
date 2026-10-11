#!/usr/bin/env python3
"""交叉编译 codexweb / devctl。

用法:
    python3 scripts/build.py                                  # linux/arm64（安卓）
    python3 scripts/build.py --goos windows --goarch amd64
    python3 scripts/build.py --out build --go /usr/local/go/bin/go
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
MODULES = ("codexweb", "devctl")


def main():
    ap = argparse.ArgumentParser(description="build codexweb / devctl")
    ap.add_argument("--goos", default="linux")
    ap.add_argument("--goarch", default="arm64")
    ap.add_argument("--out", default="build")
    ap.add_argument("--go", default=shutil.which("go") or "go")
    args = ap.parse_args()

    out = ROOT / args.out
    out.mkdir(parents=True, exist_ok=True)

    env = dict(os.environ)
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = args.goos
    env["GOARCH"] = args.goarch
    env.setdefault("GOFLAGS", "-mod=mod")

    suffix = ".exe" if args.goos == "windows" else ""
    for name in MODULES:
        if args.goos == "linux" and args.goarch == "arm64":
            target = out / (name + "-arm64" + suffix)
        else:
            target = out / ("%s-%s-%s%s" % (name, args.goos, args.goarch, suffix))
        cmd = [args.go, "build", "-trimpath", "-ldflags", "-s -w", "-o", str(target), "."]
        print("[build] %s  (%s/%s) -> %s" % (name, args.goos, args.goarch,
                                             target.relative_to(ROOT)))
        rc = subprocess.run(cmd, cwd=str(ROOT / "src" / name), env=env).returncode
        if rc != 0:
            print("[build] 失败: %s" % name, file=sys.stderr)
            return rc

    print("[build] 产物:")
    for p in sorted(out.iterdir()):
        if p.is_file():
            print("  %-24s %d bytes" % (p.name, p.stat().st_size))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
