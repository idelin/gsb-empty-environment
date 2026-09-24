"""Command line entry point: python -m lockmerge ..."""

from __future__ import annotations

import argparse
import json
import sys

from .lockfile import LockFile
from .merge import merge_locks
from .registry import Registry


def main(argv=None):
    parser = argparse.ArgumentParser(
        prog="lockmerge",
        description="Three-way merge of package lock files using a local JSON registry (no network).",
    )
    parser.add_argument("--base", required=True, help="common ancestor lock file (JSON)")
    parser.add_argument("--ours", required=True, help="our side of the merge (JSON)")
    parser.add_argument("--theirs", required=True, help="their side of the merge (JSON)")
    parser.add_argument("--registry", required=True, help="local package registry (JSON)")
    parser.add_argument("--out", help="optional path to write the merged lock file")
    args = parser.parse_args(argv)

    registry = Registry.load(args.registry)
    base = LockFile.load(args.base)
    ours = LockFile.load(args.ours)
    theirs = LockFile.load(args.theirs)

    result = merge_locks(base, ours, theirs, registry)
    report = result.to_report()
    print(json.dumps(report, indent=2, ensure_ascii=False))

    if args.out and result.ok:
        with open(args.out, "w", encoding="utf-8") as handle:
            json.dump(report["merged"], handle, indent=2, ensure_ascii=False)
            handle.write("\n")
    return 0 if result.ok else 1


if __name__ == "__main__":
    sys.exit(main())
