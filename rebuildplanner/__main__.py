"""命令行入口。

python3.11 -m rebuildplanner plan examples/packages.json examples/build.json
python3.11 -m rebuildplanner verify examples/packages.json examples/build.json plan.json
"""

from __future__ import annotations

import argparse
import json
import sys

from .model import ModelError
from .planner import build_plan, plan_to_dict, verify_plan
from .version import VersionError


def _load(paths):
    from .model import Problem

    return Problem.from_files(paths[0], paths[1])


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="rebuildplanner",
        description="离线计算满足全部目标的最小重建成本计划。",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    plan_p = sub.add_parser("plan", help="生成最小成本计划并输出 JSON")
    plan_p.add_argument("packages", help="本地包索引 packages.json")
    plan_p.add_argument("build", help="构建输入 build.json")
    plan_p.add_argument("-o", "--output", help="将计划写入文件（默认 stdout）")

    verify_p = sub.add_parser("verify", help="逐节点验证一个计划 JSON")
    verify_p.add_argument("packages")
    verify_p.add_argument("build")
    verify_p.add_argument("plan", help="待验证的计划 JSON 文件")

    args = parser.parse_args(argv)
    try:
        problem = _load((args.packages, args.build))
        if args.command == "plan":
            plan = build_plan(problem)
            payload = plan_to_dict(plan, problem)
            text = json.dumps(payload, ensure_ascii=False, indent=2)
            if args.output:
                with open(args.output, "w", encoding="utf-8") as handle:
                    handle.write(text + "\n")
                print(f"计划已写入 {args.output}，最小总成本：{plan.total_cost}", file=sys.stderr)
            else:
                print(text)
            return 0

        with open(args.plan, "r", encoding="utf-8") as handle:
            plan_payload = json.load(handle)
        errors = verify_plan(problem, plan_payload)
        if errors:
            for err in errors:
                print(f"INVALID: {err}", file=sys.stderr)
            return 1
        print(f"VALID: 计划通过全部逐节点校验，总成本 {plan_payload.get('total_cost')}")
        return 0
    except (ModelError, VersionError, FileNotFoundError, json.JSONDecodeError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
