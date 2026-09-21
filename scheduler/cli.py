"""命令行入口：读取本地 JSON，输出启发式调度计划与下界。"""

from __future__ import annotations

import argparse
import json
import sys

from .errors import InfeasibleError, InputError
from .heuristic import build_schedule
from .loader import load_build_input
from .lowerbound import lower_bound

DISCLAIMER = "注意：本工期由确定性启发式算法给出，是可行上界，未证明为全局最优。"


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="scheduler",
        description="DAG 任务资源约束调度（确定性启发式 + 下界），输入为本地 JSON。",
    )
    parser.add_argument("input", help="本地 JSON 构建输入文件路径")
    parser.add_argument("--json", action="store_true", help="以 JSON 格式输出结果")
    args = parser.parse_args(argv)

    try:
        build = load_build_input(args.input)
        result = build_schedule(build)
        bound = lower_bound(build)
    except (InputError, InfeasibleError) as exc:
        print(f"错误: {exc}", file=sys.stderr)
        return 2

    if args.json:
        payload = {
            "name": build.name,
            "capacity": {"cpu": build.capacity.cpu, "memory": build.capacity.memory},
            "packages": [{"name": p.name, "version": p.version} for p in build.packages],
            "schedule": [
                {"task": e.task_id, "start": e.start, "end": e.end}
                for e in result.entries
            ],
            "makespan_heuristic": result.makespan,
            "lower_bound": bound,
            "gap": result.makespan - bound,
            "optimal": "unknown (heuristic upper bound)",
        }
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return 0

    print(f"构建: {build.name}")
    print(f"容量: cpu={build.capacity.cpu}, memory={build.capacity.memory}")
    if build.packages:
        print("本地包:")
        for pkg in build.packages:
            print(f"  - {pkg.name} {pkg.version}")
    print("调度计划:")
    print(f"  {'任务':<16}{'开始':>6}{'结束':>6}")
    for e in result.entries:
        print(f"  {e.task_id:<16}{e.start:>6}{e.end:>6}")
    print(f"启发式工期 (makespan): {result.makespan}")
    print(f"下界 (lower bound):    {bound}")
    print(f"间隙 (gap):            {result.makespan - bound}")
    print(DISCLAIMER)
    return 0
