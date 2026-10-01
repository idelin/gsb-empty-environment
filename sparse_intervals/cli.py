"""命令行入口。输入输出均为 JSON，格式见 README.md。"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import asdict

from .core1d import Config, Timeline
from .rect2d import (
    RectConfig,
    RectTimeline,
    exact_min_partition,
    greedy_partition,
    rects_from_cells,
    verify_partition,
)


def _load(path: str):
    if path == "-":
        return json.load(sys.stdin)
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def _dump(obj) -> None:
    json.dump(obj, sys.stdout, ensure_ascii=False, indent=2)
    sys.stdout.write("\n")


def _config_from(d: dict) -> Config:
    return Config(
        id=d["id"],
        key=d["key"],
        priority=d.get("priority", 0),
        version=d.get("version", 0),
        start=d["start"],
        end=d["end"],
        value=d.get("value"),
    )


def _rect_config_from(d: dict) -> RectConfig:
    return RectConfig(
        id=d["id"],
        priority=d.get("priority", 0),
        version=d.get("version", 0),
        t0=d["t0"],
        t1=d["t1"],
        v0=d["v0"],
        v1=d["v1"],
        value=d.get("value"),
    )


def _query_from(d: dict):
    q = d["query"]
    return (q["t0"], q["t1"], q["v0"], q["v1"])


def _build_timeline(data) -> Timeline:
    tl = Timeline()
    for c in data.get("configs", []):
        tl.add(_config_from(c))
    return tl


def _segments_json(tl: Timeline):
    keys = sorted(tl._keys.keys())
    return {
        k: [asdict(s) for s in tl.segments(k)]
        for k in keys
        if tl.segments(k)
    }


def cmd_timeline(args) -> None:
    tl = _build_timeline(_load(args.input))
    out = {"segments": _segments_json(tl)}
    if args.explain:
        out["explain"] = {
            k: [asdict(e) for e in tl.explain(k)]
            for k in sorted(tl._keys.keys())
            if tl.explain(k)
        }
    _dump(out)


def cmd_revoke(args) -> None:
    tl = _build_timeline(_load(args.input))
    affected = []
    for cid in args.config_id:
        affected.append(
            {"revoked": cid, "affected": [asdict(a) for a in tl.revoke(cid)]}
        )
    _dump({"operations": affected, "segments": _segments_json(tl)})


def _rects_json(rects):
    return [asdict(r) for r in rects]


def cmd_rect(args) -> None:
    data = _load(args.input)
    query = _query_from(data)
    rt = RectTimeline(query)
    for c in data.get("configs", []):
        rt.add(_rect_config_from(c))
    grid = rt.grid
    out = {"query": list(query), "audit_errors": rt.audit()}
    if args.solver in ("greedy", "both"):
        rects = greedy_partition(grid)
        out["greedy"] = {
            "count": len(rects),
            "rects": _rects_json(rects),
            "verify_errors": verify_partition(grid, rects),
            "note": "局部贪心合并，不保证全局最少矩形数",
        }
    if args.solver in ("exact", "both"):
        res = exact_min_partition(grid.values, budget=args.budget)
        exact = {
            "optimal": res.optimal,
            "count": res.count,
            "lower_bound": res.lower_bound,
            "upper_bound": res.upper_bound,
            "nodes": res.nodes,
        }
        if res.optimal:
            exact_rects = rects_from_cells(grid, res.cells)
            exact["rects"] = _rects_json(exact_rects)
            exact["verify_errors"] = verify_partition(grid, exact_rects)
        out["exact"] = exact
    _dump(out)


def cmd_rect_revoke(args) -> None:
    data = _load(args.input)
    rt = RectTimeline(_query_from(data))
    for c in data.get("configs", []):
        rt.add(_rect_config_from(c))
    ops = []
    for cid in args.config_id:
        ops.append(
            {"revoked": cid, "affected": [asdict(a) for a in rt.revoke(cid)]}
        )
    _dump(
        {
            "operations": ops,
            "audit_errors": rt.audit(),
            "partition": _rects_json(rt.partition()),
        }
    )


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(
        prog="sparse_intervals",
        description="稀疏区间冲突修复：一维规范时间线与二维矩形分区",
    )
    sub = parser.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("timeline", help="输出一维规范时间线（可加 --explain）")
    p.add_argument("input", help="JSON 输入文件，'-' 表示 stdin")
    p.add_argument("--explain", action="store_true", help="附带每段被遮蔽规则列表")
    p.set_defaults(func=cmd_timeline)

    p = sub.add_parser("revoke", help="依次撤销配置并输出受影响区间")
    p.add_argument("input")
    p.add_argument("--config-id", action="append", required=True, help="可重复")
    p.set_defaults(func=cmd_revoke)

    p = sub.add_parser("rect", help="二维矩形分区（greedy / exact / both）")
    p.add_argument("input")
    p.add_argument("--solver", choices=["greedy", "exact", "both"], default="both")
    p.add_argument("--budget", type=int, default=100_000, help="精确求解节点预算")
    p.set_defaults(func=cmd_rect)

    p = sub.add_parser("rect-revoke", help="二维撤销并输出受影响区域与审计结果")
    p.add_argument("input")
    p.add_argument("--config-id", action="append", required=True, help="可重复")
    p.set_defaults(func=cmd_rect_revoke)

    args = parser.parse_args(argv)
    args.func(args)


if __name__ == "__main__":
    main()
