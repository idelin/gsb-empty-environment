"""命令行入口：run / verify / demo。"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import asdict

from .engine import Engine, full_recompute
from .journal import PersistentEngine


def _emit(record: dict, out) -> None:
    out.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")


def _attrs_dict(attributions) -> dict:
    return {cid: asdict(att) for cid, att in sorted(attributions.items())}


def _load_ops(path: str):
    ops = []
    with open(path, encoding="utf-8") as fh:
        for lineno, line in enumerate(fh, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            op = json.loads(line)
            op.setdefault("key", f"line-{lineno}")
            ops.append(op)
    return ops


def cmd_run(args) -> int:
    engine = PersistentEngine(args.window, args.journal)
    out = open(args.output, "w", encoding="utf-8") if args.output else sys.stdout
    try:
        for op in _load_ops(args.input):
            kind = op.get("op")
            if kind == "query":
                _emit(
                    {"type": "query_result", "attributions": _attrs_dict(engine.attributions)},
                    out,
                )
            elif kind == "query_at":
                seq = int(op["seq"])
                _emit(
                    {
                        "type": "query_result",
                        "seq": seq,
                        "attributions": _attrs_dict(engine.query_at(seq)),
                    },
                    out,
                )
            else:
                for change in engine.apply(op["key"], op):
                    _emit(change, out)
    finally:
        if out is not sys.stdout:
            out.close()
    return 0


def cmd_verify(args) -> int:
    ops = [op for op in _load_ops(args.input) if op.get("op") not in ("query", "query_at")]
    engine = Engine(args.window)
    for op in ops:
        engine.apply(op)
    expected = full_recompute(ops, args.window)
    if engine.attributions == expected:
        print(f"OK: 增量与全量一致（{len(expected)} 个成交，{len(ops)} 个操作）")
        return 0
    print("MISMATCH: 增量与全量不一致", file=sys.stderr)
    for cid in sorted(set(engine.attributions) | set(expected)):
        got, want = engine.attributions.get(cid), expected.get(cid)
        if got != want:
            print(f"  {cid}: 增量={got} 全量={want}", file=sys.stderr)
    return 1


def cmd_demo(args) -> int:
    """验收边界示例：触点a@2、成交@5、窗口3 含 a；迟到触点b@4 改归 b。"""
    engine = Engine(window=3)
    ops = [
        {"op": "event", "event_id": "t1", "version": 1, "subject": "u1",
         "time": 2, "kind": "touchpoint", "channel": "a"},
        {"op": "event", "event_id": "c1", "version": 1, "subject": "u1",
         "time": 5, "kind": "conversion", "amount": 100},
        {"op": "event", "event_id": "t2", "version": 1, "subject": "u1",
         "time": 4, "kind": "touchpoint", "channel": "b"},
    ]
    for op in ops:
        for change in engine.apply(op):
            _emit(change, sys.stdout)
    _emit({"type": "query_result", "attributions": _attrs_dict(engine.attributions)}, sys.stdout)
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="attribution", description="用户旅程去重归因（窗口内最后触点，版本覆盖，身份图）"
    )
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_run = sub.add_parser("run", help="处理操作流并输出归因变更（JSONL）")
    p_run.add_argument("--window", type=int, required=True, help="归因窗口长度（左闭右闭）")
    p_run.add_argument("--input", required=True, help="操作文件（JSONL）")
    p_run.add_argument("--journal", default=None, help="持久日志路径（可选）")
    p_run.add_argument("--output", default=None, help="输出文件（默认 stdout）")
    p_run.set_defaults(fn=cmd_run)

    p_verify = sub.add_parser("verify", help="增量结果与全量重算对照")
    p_verify.add_argument("--window", type=int, required=True)
    p_verify.add_argument("--input", required=True)
    p_verify.set_defaults(fn=cmd_verify)

    p_demo = sub.add_parser("demo", help="运行验收边界示例（合成数据）")
    p_demo.set_defaults(fn=cmd_demo)

    args = parser.parse_args(argv)
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main())
