"""migsched 命令行界面。

子命令：
  plan    读取迁移规格 JSON，输出调度计划 JSON
  verify  校验计划 JSON 是否满足全部约束
  demo    运行验收示例（A 写 x，B 读 x，C 写 y，k=2）
"""

from __future__ import annotations

import argparse
import json
import sys

from .scheduler import CycleError, Migration, plan, verify_schedule, build_predecessors


def _load_spec(text: str):
    spec = json.loads(text)
    if not isinstance(spec, dict) or "migrations" not in spec:
        raise ValueError("输入必须是含 migrations 字段的 JSON 对象")
    migrations = []
    for entry in spec["migrations"]:
        migrations.append(
            Migration(
                name=entry["name"],
                reads=frozenset(entry.get("reads", [])),
                writes=frozenset(entry.get("writes", [])),
                deps=tuple(entry.get("deps", [])),
            )
        )
    return spec.get("k"), migrations


def _read_text(path: str) -> str:
    if path == "-":
        return sys.stdin.read()
    with open(path, "r", encoding="utf-8") as fh:
        return fh.read()


def _emit(obj, compact: bool) -> None:
    if compact:
        print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")))
    else:
        print(json.dumps(obj, ensure_ascii=False, indent=2))


def _cmd_plan(args) -> int:
    try:
        spec_k, migrations = _load_spec(_read_text(args.input))
        k = args.k if args.k is not None else spec_k
        if k is None:
            raise ValueError("未指定 k：请在输入文件中提供 \"k\" 或使用 --k")
        result = plan(
            migrations, k, exact_limit=args.exact_limit, budget=args.budget
        )
    except (ValueError, CycleError, KeyError, json.JSONDecodeError) as exc:
        print(f"错误: {exc}", file=sys.stderr)
        return 2
    _emit(result, args.compact)
    return 0


def _cmd_verify(args) -> int:
    try:
        spec_k, migrations = _load_spec(_read_text(args.input))
        k = args.k if args.k is not None else spec_k
        if k is None:
            raise ValueError("未指定 k：请在输入文件中提供 \"k\" 或使用 --k")
        plan_doc = json.loads(_read_text(args.plan))
        name_to_idx = {m.name: i for i, m in enumerate(migrations)}
        rounds = []
        for rnd in plan_doc["rounds"]:
            rounds.append([name_to_idx[name] for name in rnd])
        preds, _, _ = build_predecessors(migrations)
        result = verify_schedule(migrations, preds, rounds, k)
    except (ValueError, CycleError, KeyError, json.JSONDecodeError) as exc:
        print(f"错误: {exc}", file=sys.stderr)
        return 2
    _emit(result, args.compact)
    return 0 if result["ok"] else 1


def _cmd_demo(args) -> int:
    migrations = [
        Migration("A", frozenset(), frozenset({"x"})),
        Migration("B", frozenset({"x"}), frozenset()),
        Migration("C", frozenset(), frozenset({"y"})),
    ]
    result = plan(migrations, k=2)
    result["notes"].insert(
        0, "验收示例：A 写 x，B 读 x，C 写 y，原序 A B C，k=2。"
    )
    _emit(result, args.compact)
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="migsched",
        description="非交换迁移变更排序：生成满足约束的最少轮次调度计划",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    plan_p = sub.add_parser("plan", help="生成调度计划")
    plan_p.add_argument("input", help="输入 JSON 文件路径，'-' 表示标准输入")
    plan_p.add_argument("--k", type=int, default=None, help="每轮容量上限（覆盖输入中的 k）")
    plan_p.add_argument("--exact-limit", type=int, default=16,
                        help="精确求解的迁移数上限（默认 16）")
    plan_p.add_argument("--budget", type=int, default=200000,
                        help="精确搜索的状态扩展预算（默认 200000）")
    plan_p.add_argument("--compact", action="store_true", help="单行 JSON 输出")
    plan_p.set_defaults(func=_cmd_plan)

    verify_p = sub.add_parser("verify", help="校验已有计划的可行性")
    verify_p.add_argument("input", help="迁移规格 JSON 文件路径")
    verify_p.add_argument("plan", help="计划 JSON 文件路径（含 rounds 字段）")
    verify_p.add_argument("--k", type=int, default=None, help="每轮容量上限（覆盖输入中的 k）")
    verify_p.add_argument("--compact", action="store_true", help="单行 JSON 输出")
    verify_p.set_defaults(func=_cmd_verify)

    demo_p = sub.add_parser("demo", help="运行验收示例 A/B/C，k=2")
    demo_p.add_argument("--compact", action="store_true", help="单行 JSON 输出")
    demo_p.set_defaults(func=_cmd_demo)

    return parser


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)
