"""Command line interface: plan minimal build sets from local JSON inputs."""

from __future__ import annotations

import argparse
import json
import sys

from .cache import Cache
from .model import BuildInput, PackageIndex
from .resolver import BuildsysError, resolve


def _plan_one(index, build_input, cache):
    resolution = resolve(index, build_input.context, build_input.roots)
    entries = cache.materialize(resolution, build_input)
    return resolution, entries


def _print_summary(build_input, resolution, entries):
    ctx = build_input.context
    print(f"== target {ctx.os}-{ctx.arch} (stage={ctx.stage}) ==")
    print(f"minimal build set ({len(resolution.build_order)} packages):")
    for domain, name in resolution.build_order:
        version = resolution.selected[(domain, name)]
        print(f"  [{domain:6s}] {name} {version}")
    created = sum(1 for e in entries if e.status == "created")
    reused = sum(1 for e in entries if e.status == "cached")
    print(f"cache: {created} created, {reused} reused")
    skipped = [e for e in resolution.edges if not e.included]
    print(f"condition evidence: {len(resolution.edges)} edges evaluated, "
          f"{len(skipped)} excluded by false conditions")
    print()


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="buildsys",
        description="Plan minimal build sets from local JSON (no network).",
    )
    sub = parser.add_subparsers(dest="command", required=True)
    plan = sub.add_parser("plan", help="resolve inputs and populate the cache")
    plan.add_argument("--packages", required=True, help="local package index JSON")
    plan.add_argument("--input", required=True, action="append", dest="inputs",
                      help="build input JSON (repeatable for multiple targets)")
    plan.add_argument("--cache-dir", required=True, help="shared cache root")
    plan.add_argument("--evidence", required=True, help="write condition evidence JSON here")
    args = parser.parse_args(argv)

    index = PackageIndex.from_json(args.packages)
    cache = Cache(args.cache_dir)
    report = {"packages": args.packages, "cache_dir": args.cache_dir, "targets": []}
    try:
        for input_path in args.inputs:
            build_input = BuildInput.from_json(input_path)
            resolution, entries = _plan_one(index, build_input, cache)
            _print_summary(build_input, resolution, entries)
            ctx = build_input.context
            report["targets"].append({
                "input": input_path,
                "context": {"os": ctx.os, "arch": ctx.arch, "stage": ctx.stage},
                "host_key": build_input.host_key,
                "target_key": build_input.target_key,
                "edges": [e.to_json() for e in resolution.edges],
                "selected": resolution.selected_json(),
                "build_order": [f"{d}/{n}" for d, n in resolution.build_order],
                "cache": [e.to_json() for e in entries],
            })
    except BuildsysError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    with open(args.evidence, "w", encoding="utf-8") as handle:
        json.dump(report, handle, indent=2)
        handle.write("\n")
    print(f"evidence written to {args.evidence}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
