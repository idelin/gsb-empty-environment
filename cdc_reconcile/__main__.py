"""用法: python -m cdc_reconcile <replica_a.json> <replica_b.json>"""
from __future__ import annotations

import json
import sys

from .reconcile import ReplicaStream, compare
from .watermark import WatermarkTracker


def render(report, name_a: str, name_b: str) -> str:
    lines = [f"共同水位: {report.common_watermark}"]

    if report.lagging:
        lag = report.lagging
        lines.append(
            f"未追平: 副本 {lag['lagging_side']} 落后 "
            f"(对侧 {lag['ahead_side']} 进度 {lag['ahead_progress']}, "
            f"待追平事件 {lag['pending_count']} 条: {lag['pending_commit_seqs']})"
        )
        lines.append("  -> 水位之上的事件不参与比较，不报数据错误")
    else:
        lines.append("双副本进度一致，无未追平")

    lines.append(f"一致实体数: {report.consistent_entities}")

    lines.append(f"缺事件 ({len(report.missing_events)}):")
    for item in report.missing_events:
        keys = [k for k in item if k.startswith("missing_on_")]
        detail = ", ".join(f"{k.split('missing_on_')[1]} 缺 {item[k]}" for k in keys)
        lines.append(f"  {item['table']}:{item['pk']}  {detail}")

    lines.append(f"顺序冲突 ({len(report.ordering_conflicts)}):")
    for item in report.ordering_conflicts:
        lines.append(
            f"  {item['table']}:{item['pk']}  "
            f"{name_a} 顺序 {item[f'order_{name_a}']} vs {name_b} 顺序 {item[f'order_{name_b}']}"
        )

    lines.append(f"值分歧 ({len(report.value_divergences)}):")
    for item in report.value_divergences:
        lines.append(
            f"  {item['table']}:{item['pk']}  "
            f"{name_a}={json.dumps(item[f'value_{name_a}'], ensure_ascii=False)} "
            f"{name_b}={json.dumps(item[f'value_{name_b}'], ensure_ascii=False)}"
        )

    lines.append(f"同值缺事件 ({len(report.same_value_missing_events)}):")
    for item in report.same_value_missing_events:
        keys = [k for k in item if k.startswith("missing_on_")]
        detail = ", ".join(f"{k.split('missing_on_')[1]} 缺 {item[k]}" for k in keys)
        lines.append(
            f"  {item['table']}:{item['pk']}  最终值相同 "
            f"{json.dumps(item['final_value'], ensure_ascii=False)}，{detail}"
        )

    return "\n".join(lines)


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(__doc__, file=sys.stderr)
        return 2
    stream_a = ReplicaStream.from_file(argv[1])
    stream_b = ReplicaStream.from_file(argv[2])
    tracker = WatermarkTracker()
    report = compare(stream_a, stream_b, tracker=tracker)
    print(render(report, stream_a.name, stream_b.name))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
