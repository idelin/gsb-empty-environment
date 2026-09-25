"""运行合成样例：对比 t1 -> t2，输出最小因果切片报告（含重放验证）。"""

import json

from causal.diff import ADDED, REMOVED, SUPPORT_CHANGED, compare
from sample_data import build_snapshots

_CHANGE_TITLES = {
    ADDED: "新增结论",
    REMOVED: "失效结论",
    SUPPORT_CHANGED: "结果相同但支持来源改变",
}


def render(report) -> str:
    lines = [f"===== 推导结论对比报告: {report.label_t1} -> {report.label_t2} =====", ""]
    for change_type in (ADDED, REMOVED, SUPPORT_CHANGED):
        group = [c for c in report.changes if c.change == change_type]
        if not group:
            continue
        lines.append(f"[{_CHANGE_TITLES[change_type]}]")
        for c in group:
            lines.append(f"  结论 {c.conclusion_id}")
            lines.append(f"    原因类别: {', '.join(c.causes)}")
            lines.append("    最小因果切片:")
            for d in c.slice:
                lines.append(f"      - {d.describe()}")
            lines.append(f"    步骤(t1): {', '.join(c.steps_t1) or '(无)'}")
            lines.append(f"    步骤(t2): {', '.join(c.steps_t2) or '(无)'}")
            verdict = "通过" if c.replay_ok else "失败"
            lines.append(f"    重放验证: {verdict} (t1 + 切片 => 重放该变化)")
        lines.append("")
    lines.append("[无关输入（不进入任何解释）]")
    for d in report.irrelevant_diffs:
        lines.append(f"  - {d.describe()}")
    return "\n".join(lines)


def to_evidence(report) -> dict:
    """可重放证据：每条变化的最小切片与重放结果，可机器校验。"""
    return {
        "from": report.label_t1,
        "to": report.label_t2,
        "changes": [
            {
                "conclusion": c.conclusion_id,
                "change": c.change,
                "causes": list(c.causes),
                "slice": [
                    {"kind": d.kind, "id": d.target_id,
                     "old_version": d.old_version, "new_version": d.new_version}
                    for d in c.slice
                ],
                "steps_t1": list(c.steps_t1),
                "steps_t2": list(c.steps_t2),
                "replay_ok": c.replay_ok,
            }
            for c in report.changes
        ],
        "irrelevant_inputs": [
            {"kind": d.kind, "id": d.target_id} for d in report.irrelevant_diffs
        ],
    }


def main():
    t1, t2 = build_snapshots()
    report = compare(t1, t2)
    print(render(report))
    print()
    print("===== 可重放证据 (JSON) =====")
    print(json.dumps(to_evidence(report), ensure_ascii=False, indent=2))
    ok = all(c.replay_ok for c in report.changes)
    print()
    print(f"全部切片重放{'通过' if ok else '存在失败'}。")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
