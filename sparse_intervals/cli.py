"""命令行入口。

  python -m sparse_intervals timeline [-i rules.json] [--revoke RID]... [--explain]
  python -m sparse_intervals rect     [-i rules2d.json] [--revoke RID]...
                                      [--exact] [--budget N] [--dump-grid] [--verify]

输入输出均为 JSON，格式详见 README.md。
"""

from __future__ import annotations

import argparse
import json
import sys

from .timeline1d import Rule, Timeline
from .rect2d import Rule2D, RectEngine


def _load_json(path):
    if path:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    return json.load(sys.stdin)


def _dump(obj) -> None:
    json.dump(obj, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


def _rule1d(d: dict) -> Rule:
    return Rule(
        rid=str(d["id"]),
        priority=int(d["priority"]),
        version=int(d["version"]),
        start=d["start"],
        end=d["end"],
        value=d.get("value"),
    )


def _rule2d(d: dict) -> Rule2D:
    return Rule2D(
        rid=str(d["id"]),
        priority=int(d["priority"]),
        version=int(d["version"]),
        t0=d["t0"],
        t1=d["t1"],
        v0=d["v0"],
        v1=d["v1"],
        value=d.get("value"),
    )


def cmd_timeline(args) -> int:
    data = _load_json(args.input)
    tl = Timeline()
    for d in data.get("rules", []):
        tl.add(_rule1d(d))
    out = {
        "segments": [s.to_dict(args.explain) for s in tl.segments()],
        "merge_note": "同值相邻段已合并，段数对生效值时间线而言最少。",
    }
    revs = []
    for rid in args.revoke:
        try:
            affected = tl.revoke(rid)
        except KeyError:
            revs.append({"rid": rid, "error": "unknown rule id"})
            continue
        revs.append({
            "rid": rid,
            "affected": affected,
            "segments": [s.to_dict(args.explain) for s in tl.segments()],
        })
    if revs:
        out["revocations"] = revs
    _dump(out)
    return 0


def cmd_rect(args) -> int:
    data = _load_json(args.input)
    q = data["query"]
    eng = RectEngine(q["t0"], q["t1"], q["v0"], q["v1"])
    for d in data.get("rules", []):
        eng.add(_rule2d(d))
    out = {
        "query": q,
        "rects": eng.partition(),
        "merge_note": "贪心条带合并，不保证全局最少矩形数；"
                      "用 --exact 获取小网格精确最小覆盖。",
    }
    out["rect_count"] = len(out["rects"])
    if args.dump_grid:
        out["grid"] = eng.grid_dump()
    if args.exact:
        out["exact"] = eng.exact_partition(budget=args.budget)
    if args.verify:
        out["verification"] = {"errors": eng.verify()}
    revs = []
    for rid in args.revoke:
        try:
            r = eng.revoke(rid, verify=True)
        except KeyError:
            revs.append({"rid": rid, "error": "unknown rule id"})
            continue
        revs.append(r)
    if revs:
        out["revocations"] = revs
        out["rects"] = eng.partition()
        out["rect_count"] = len(out["rects"])
        if args.verify:
            out["verification"] = {"errors": eng.verify()}
    _dump(out)
    return 0


def main(argv=None) -> int:
    p = argparse.ArgumentParser(
        prog="sparse_intervals",
        description="稀疏区间冲突修复：一维规范时间线 + 二维矩形分区")
    sub = p.add_subparsers(dest="cmd", required=True)

    t = sub.add_parser("timeline", help="一维规范时间线")
    t.add_argument("--input", "-i", help="输入 JSON 文件（缺省读 stdin）")
    t.add_argument("--revoke", action="append", default=[],
                   metavar="RID", help="撤销指定规则（可重复）")
    t.add_argument("--explain", action="store_true",
                   help="每段输出 winners/shadowed 解释")
    t.set_defaults(func=cmd_timeline)

    r = sub.add_parser("rect", help="二维矩形分区")
    r.add_argument("--input", "-i", help="输入 JSON 文件（缺省读 stdin）")
    r.add_argument("--revoke", action="append", default=[],
                   metavar="RID", help="撤销指定规则（可重复）")
    r.add_argument("--exact", action="store_true",
                   help="运行小网格精确最小矩形覆盖求解器")
    r.add_argument("--budget", type=int, default=200_000,
                   help="精确求解器搜索节点预算（默认 200000）")
    r.add_argument("--dump-grid", action="store_true", help="输出压缩网格")
    r.add_argument("--verify", action="store_true",
                   help="用独立验证器检查输出分区")
    r.set_defaults(func=cmd_rect)

    args = p.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
