#!/usr/bin/env python3
"""去重：按大小预筛 + SHA-256 精确比对，默认只报告不删除。

用法:
  toolbox_run { tool: "dedupe-by-hash", args: ["D:\\素材", "--apply", "--keep", "newest"] }

参数:
  目录            必填，递归扫描
  --apply         真正删除（默认 dry-run 只列清单）
  --keep newest|oldest|first   重复组里保留哪一个（默认 newest）
  --min-size N    只处理 >= N 字节的文件（默认 1）
  --csv PATH      把结果写成 CSV
"""
import argparse
import csv
import hashlib
import os
import sys
from collections import defaultdict
from datetime import datetime


def sha256(path, chunk=1 << 20):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        while True:
            block = f.read(chunk)
            if not block:
                break
            h.update(block)
    return h.hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("root")
    ap.add_argument("--apply", action="store_true")
    ap.add_argument("--keep", choices=["newest", "oldest", "first"], default="newest")
    ap.add_argument("--min-size", type=int, default=1)
    ap.add_argument("--csv", dest="csv_path")
    args = ap.parse_args()

    if not os.path.isdir(args.root):
        print(f"不是目录: {args.root}", file=sys.stderr)
        return 2

    by_size = defaultdict(list)
    for base, _dirs, files in os.walk(args.root):
        for name in files:
            path = os.path.join(base, name)
            try:
                size = os.path.getsize(path)
            except OSError:
                continue
            if size >= args.min_size:
                by_size[size].append(path)

    groups = []
    for size, paths in by_size.items():
        if len(paths) < 2:
            continue
        by_hash = defaultdict(list)
        for path in paths:
            try:
                by_hash[sha256(path)].append(path)
            except OSError:
                continue
        for digest, same in by_hash.items():
            if len(same) > 1:
                groups.append((size, digest, same))

    if not groups:
        print("没有找到完全重复的文件")
        return 0

    rows, freed, planned = [], 0, 0
    for size, digest, same in sorted(groups, key=lambda g: -g[0] * (len(g[2]) - 1)):
        if args.keep == "newest":
            same.sort(key=lambda p: os.path.getmtime(p), reverse=True)
        elif args.keep == "oldest":
            same.sort(key=lambda p: os.path.getmtime(p))
        keep, dupes = same[0], same[1:]
        planned += len(dupes)
        freed += size * len(dupes)
        print(f"\n[{size} B] 保留 {keep}")
        for path in dupes:
            print(("  删除 " if args.apply else "  重复 ") + path)
            rows.append([size, digest, keep, path, "deleted" if args.apply else "duplicate"])
            if args.apply:
                try:
                    os.remove(path)
                except OSError as error:
                    print(f"    失败: {error}", file=sys.stderr)

    if args.csv_path:
        with open(args.csv_path, "w", newline="", encoding="utf-8") as f:
            writer = csv.writer(f)
            writer.writerow(["size", "sha256", "kept", "duplicate", "action"])
            writer.writerows(rows)

    print(f"\n共 {len(groups)} 组重复，{'已删除' if args.apply else '可删除'} {planned} 个文件，释放约 {freed / 1024 / 1024:.1f} MB")
    if not args.apply:
        print("这是 dry-run；加 --apply 才会真的删除")
    return 0


if __name__ == "__main__":
    sys.exit(main())
