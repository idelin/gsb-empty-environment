"""Command line entry point:

    python -m lockmerge --base b.json --ours o.json --theirs t.json \
        --registry registry.json --out merged.json
"""

from __future__ import annotations

import argparse
import json
import sys

from .merger import merge_locks
from .models import load_lockfile, load_registry


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="lockmerge",
        description="Offline three-way merge for package lock files.")
    parser.add_argument("--base", required=True, help="common ancestor lock")
    parser.add_argument("--ours", required=True, help="our branch lock")
    parser.add_argument("--theirs", required=True, help="their branch lock")
    parser.add_argument("--registry", required=True,
                        help="local JSON package registry (build inputs)")
    parser.add_argument("--out", help="write merged result JSON here")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    result = merge_locks(
        load_lockfile(args.base),
        load_lockfile(args.ours),
        load_lockfile(args.theirs),
        load_registry(args.registry),
    )
    payload = json.dumps(result.to_dict(), indent=2, ensure_ascii=False)
    if args.out:
        with open(args.out, "w", encoding="utf-8") as fh:
            fh.write(payload + "\n")
    else:
        print(payload)
    return 0 if result.status == "merged" else 2


if __name__ == "__main__":
    sys.exit(main())
