from __future__ import annotations

import argparse
import json
import sys

from .compare import compare_trees
from .evidence import build_bundle, read_bundle, write_bundle
from .rules import load_rules
from .verify import verify_bundle


def _load_rules_arg(path: str | None):
    if path is None:
        return load_rules(None)
    with open(path, encoding="utf-8") as handle:
        return load_rules(json.load(handle))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="reproduce_compare")
    sub = parser.add_subparsers(dest="command", required=True)

    cmp_p = sub.add_parser("compare", help="compare two tree roots and emit an evidence bundle")
    cmp_p.add_argument("old_root")
    cmp_p.add_argument("new_root")
    cmp_p.add_argument("--rules", help="JSON rules document")
    cmp_p.add_argument("--evidence", required=True, help="output evidence bundle path")
    cmp_p.add_argument("--max-depth", type=int, default=8)
    cmp_p.add_argument("--max-bytes", type=int, default=256 * 1024 * 1024)
    cmp_p.add_argument("--max-entries", type=int, default=100_000)

    ver_p = sub.add_parser("verify", help="independently verify an evidence bundle")
    ver_p.add_argument("old_root")
    ver_p.add_argument("new_root")
    ver_p.add_argument("--evidence", required=True)
    ver_p.add_argument("--max-depth", type=int, default=8)
    ver_p.add_argument("--max-bytes", type=int, default=256 * 1024 * 1024)
    ver_p.add_argument("--max-entries", type=int, default=100_000)

    args = parser.parse_args(argv)

    from .archive import Budget

    budget = Budget(max_depth=args.max_depth, max_uncompressed_bytes=args.max_bytes,
                    max_entries=args.max_entries)

    if args.command == "compare":
        ruleset = _load_rules_arg(args.rules)
        result = compare_trees(args.old_root, args.new_root, ruleset, budget)
        write_bundle(args.evidence, build_bundle(result, ruleset))
        print(json.dumps(result.to_evidence(), indent=2, ensure_ascii=False, sort_keys=True))
        return 0 if result.status != "undeterminable" else 2

    bundle = read_bundle(args.evidence)
    verdict = verify_bundle(bundle, args.old_root, args.new_root, budget)
    print(json.dumps({"ok": verdict.ok, "recomputed_status": verdict.recomputed_status,
                      "problems": verdict.problems}, indent=2, ensure_ascii=False))
    return 0 if verdict.ok else 3


if __name__ == "__main__":
    sys.exit(main())
