from __future__ import annotations

import argparse
import json

from .planner import dry_run_plan, plan_to_dict
from .sample import sample_catalog


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Read-only synthetic lineage deletion/recomputation dry run."
    )
    parser.add_argument(
        "--subject",
        default="A-001",
        help="canonical subject id or an explicit alias from the synthetic catalog",
    )
    args = parser.parse_args()
    plan = dry_run_plan(args.subject, sample_catalog())
    print(json.dumps(plan_to_dict(plan), ensure_ascii=False, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
