"""CLI: resolve a build request against a local JSON registry."""
from __future__ import annotations

import argparse
import json
import sys

from .cache import BuildCache
from .registry import Registry
from .resolver import ConflictError, resolve
from .version import Constraint


def load_request(path):
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
    context = {
        "host_os": data["host"]["os"],
        "host_arch": data["host"]["arch"],
        "target_os": data["target"]["os"],
        "target_arch": data["target"]["arch"],
        "stage": data.get("stage", "build"),
    }
    roots = [
        (item["name"], Constraint.parse(item.get("constraint", "")))
        for item in data["roots"]
    ]
    return context, roots


def report_dict(resolution) -> dict:
    return {
        "context": resolution.context,
        "build_set": [
            {
                "name": n.name,
                "version": n.version,
                "kind": n.kind,
                "partition": n.partition,
                "cache_key": list(n.cache_key),
            }
            for n in resolution.nodes
        ],
        "evidence": [
            {
                "package": e.package,
                "dependency": e.dependency,
                "constraint": e.constraint,
                "condition": e.condition,
                "checks": [
                    {
                        "field": c.field,
                        "required": list(c.required),
                        "actual": c.actual,
                        "passed": c.passed,
                    }
                    for c in e.checks
                ],
                "taken": e.taken,
            }
            for e in resolution.evidence
        ],
        "pruned": [f"{e.package} -> {e.dependency}" for e in resolution.pruned],
    }


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="buildset",
        description="Resolve the minimal build set from local JSON inputs.",
    )
    parser.add_argument("--packages", required=True, help="registry JSON file")
    parser.add_argument("--request", required=True, help="build request JSON file")
    parser.add_argument(
        "--cache-dir",
        help="cache root; host tools and target artifacts are stored separately",
    )
    args = parser.parse_args(argv)

    registry = Registry.load(args.packages)
    context, roots = load_request(args.request)
    try:
        resolution = resolve(registry, roots, context)
    except ConflictError as exc:
        json.dump(
            {
                "error": str(exc),
                "conflict": exc.name,
                "requirements": [
                    {"constraint": str(c), "source": s} for c, s in exc.requirements
                ],
            },
            sys.stdout,
            indent=2,
        )
        sys.stdout.write("\n")
        return 1

    report = report_dict(resolution)
    if args.cache_dir:
        cache = BuildCache(args.cache_dir)
        hits, misses = cache.plan(resolution.nodes)
        for node in misses:
            cache.store(node)
        report["cache"] = {
            "root": args.cache_dir,
            "hits": [str(cache.path_for(n)) for n in hits],
            "stored": [str(cache.path_for(n)) for n in misses],
        }
    json.dump(report, sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
