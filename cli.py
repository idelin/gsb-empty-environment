"""命令行入口：按主体撤回的聚合物化视图。

子命令：
  apply    从 JSONL 文件（或 stdin）逐条应用命令，追加写入日志，并逐条输出增量结果
  query    重放日志后输出查询结果（sum/count/median，可按分组过滤）
  verify   对照增量结果与全量重算结果
  snapshot 将当前状态快照为 JSON 文件；配合 --snapshot 加载可实现持久化重启

示例：
  python3 cli.py apply --db view.journal < commands.jsonl
  python3 cli.py query --db view.journal
  python3 cli.py query --db view.journal --group g1
  python3 cli.py verify --db view.journal
  python3 cli.py snapshot --db view.journal --out snap.json
  python3 cli.py query --snapshot snap.json
"""

from __future__ import annotations

import argparse
import json
import os
import sys

from retractable_view import RetractableView


def load_view(args) -> RetractableView:
    """从快照或命令日志恢复视图（持久化重启）。"""
    if getattr(args, "snapshot", None):
        with open(args.snapshot, "r", encoding="utf-8") as fh:
            return RetractableView.load_state(json.load(fh))
    view = RetractableView()
    if args.db and os.path.exists(args.db):
        with open(args.db, "r", encoding="utf-8") as fh:
            for lineno, line in enumerate(fh, 1):
                line = line.strip()
                if not line:
                    continue
                try:
                    view.apply_command(json.loads(line))
                except Exception as exc:
                    raise SystemExit(f"日志 {args.db} 第 {lineno} 行回放失败: {exc}")
    return view


def cmd_apply(args) -> int:
    view = load_view(args)
    source = open(args.input, "r", encoding="utf-8") if args.input else sys.stdin
    journal = open(args.db, "a", encoding="utf-8") if args.db else None
    try:
        for lineno, line in enumerate(source, 1):
            line = line.strip()
            if not line:
                continue
            cmd = json.loads(line)
            applied, reason = view.apply_command(cmd)
            if journal is not None:
                journal.write(json.dumps(cmd, ensure_ascii=False) + "\n")
                journal.flush()
            out = {
                "line": lineno,
                "applied": applied,
                "reason": reason,
                "result": view.query(args.group),
            }
            print(json.dumps(out, ensure_ascii=False))
    finally:
        if args.input:
            source.close()
        if journal is not None:
            journal.close()
    return 0


def cmd_query(args) -> int:
    view = load_view(args)
    print(json.dumps(view.query(args.group), ensure_ascii=False))
    return 0


def cmd_verify(args) -> int:
    view = load_view(args)
    report = view.verify()
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return 0 if report["ok"] else 1


def cmd_snapshot(args) -> int:
    view = load_view(args)
    with open(args.out, "w", encoding="utf-8") as fh:
        json.dump(view.dump_state(), fh, ensure_ascii=False, indent=2)
    print(f"快照已写入 {args.out}", file=sys.stderr)
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="按主体撤回的聚合物化视图")
    sub = parser.add_subparsers(dest="command", required=True)

    def add_common(p):
        p.add_argument("--db", help="命令日志（JSONL）路径，用于持久化与重启回放")
        p.add_argument("--snapshot", help="从快照 JSON 恢复（优先于 --db）")

    p_apply = sub.add_parser("apply", help="应用 JSONL 命令并输出增量结果")
    add_common(p_apply)
    p_apply.add_argument("input", nargs="?", help="命令文件（JSONL），缺省读 stdin")
    p_apply.add_argument("--group", help="输出指定分组的增量结果")
    p_apply.set_defaults(func=cmd_apply)

    p_query = sub.add_parser("query", help="查询 sum/count/median")
    add_common(p_query)
    p_query.add_argument("--group", help="仅查询指定分组")
    p_query.set_defaults(func=cmd_query)

    p_verify = sub.add_parser("verify", help="增量结果与全量重算对照")
    add_common(p_verify)
    p_verify.set_defaults(func=cmd_verify)

    p_snap = sub.add_parser("snapshot", help="导出状态快照 JSON")
    add_common(p_snap)
    p_snap.add_argument("--out", required=True, help="快照输出路径")
    p_snap.set_defaults(func=cmd_snapshot)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
