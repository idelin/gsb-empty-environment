"""Command line interface: three-way merge of lock files from local JSON.

Usage:
    python -m lockmerge BASE OURS THEIRS [-o OUTPUT]

Exit status is 0 when the merge is clean, 1 when conflicts or unresolvable
items remain, and 2 on input/usage errors.
"""

from __future__ import annotations

import argparse
import json
import sys

from .lockfile import LockfileError, load_lockfile
from .merge import merge_lockfiles
from .versions import VersionError


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="lockmerge",
        description="Three-way merge of lock files (local JSON only; no network access).",
    )
    parser.add_argument("base", help="common ancestor lock file (JSON)")
    parser.add_argument("ours", help="our side of the merge (JSON)")
    parser.add_argument("theirs", help="their side of the merge (JSON)")
    parser.add_argument("-o", "--output", help="write the merge report JSON here "
                        "(default: stdout)")
    return parser


def main(argv=None) -> int:
    args = _build_parser().parse_args(argv)
    try:
        base = load_lockfile(args.base)
        ours = load_lockfile(args.ours)
        theirs = load_lockfile(args.theirs)
    except (LockfileError, VersionError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    result = merge_lockfiles(base, ours, theirs)
    report = json.dumps(result.to_dict(), indent=2, sort_keys=False) + "\n"

    if args.output:
        with open(args.output, "w", encoding="utf-8") as fh:
            fh.write(report)
    else:
        sys.stdout.write(report)

    if result.clean:
        print("merge clean: all changes auto-merged", file=sys.stderr)
        return 0
    print(f"merge needs manual resolution: {len(result.conflicts)} conflict(s), "
          f"{len(result.unresolvable)} unresolvable item(s)", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
