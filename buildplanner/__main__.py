"""CLI: python -m buildplanner <packages.json|dir> [--json]"""

from __future__ import annotations

import argparse
import json
import sys

from .loader import InputError, load_packages
from .planner import build_plan, render_plan


def _plan_dict(packages, plan) -> dict:
    return {
        "ok": plan.ok,
        "stages": [
            {
                "stage": stage_idx,
                "groups": [
                    {
                        "id": gid,
                        "members": list(plan.groups[gid].members),
                        "cyclic": plan.groups[gid].cyclic,
                        "internal_order": plan.groups[gid].internal_order,
                        "status": "blocked"
                        if gid in plan.blocked_groups
                        else "executable",
                    }
                    for gid in group_ids
                ],
            }
            for stage_idx, group_ids in enumerate(plan.stages)
        ],
        "cross_stage_cycles": [
            {
                "artifact": c.artifact,
                "generator": c.generator,
                "generator_stage": c.generator_stage,
                "consumer": c.consumer,
                "consumer_stage": c.consumer_stage,
                "dependency_path": list(c.dependency_path),
                "evidence": c.describe(packages),
            }
            for c in plan.cycles
        ],
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="buildplanner",
        description="Stage local packages into co-compilation groups; "
        "reject cross-stage generator cycles with evidence.",
    )
    parser.add_argument("input", help="JSON file or directory of package JSON files")
    parser.add_argument("--json", action="store_true", help="emit machine-readable plan")
    args = parser.parse_args(argv)

    try:
        packages = load_packages(args.input)
    except InputError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    plan = build_plan(packages)
    if args.json:
        print(json.dumps(_plan_dict(packages, plan), indent=2))
    else:
        print(render_plan(packages, plan))
        if plan.ok:
            print("plan: OK - all groups executable")
        else:
            print(
                f"plan: REJECTED - {len(plan.cycles)} cross-stage cycle(s); "
                f"{len(plan.executable_groups)} unrelated group(s) remain executable"
            )
    return 0 if plan.ok else 1


if __name__ == "__main__":
    sys.exit(main())
