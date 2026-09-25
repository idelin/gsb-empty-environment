"""运行样例：比较 t1 -> t2 两个观察时刻，输出最小因果切片报告。"""
from causaldiff import (
    format_key,
    GROUP_INVALIDATED,
    GROUP_NEW_FACT,
    GROUP_RULE_CHANGE,
    diff_snapshots,
    format_atom,
)
from sample_data import snapshot_t1, snapshot_t2

GROUP_ORDER = (GROUP_NEW_FACT, GROUP_RULE_CHANGE, GROUP_INVALIDATED)


def _fmt_change(idx, ch, base_name, direction):
    groups = "、".join(g for g in GROUP_ORDER if g in ch.groups)
    lines = [f"  {idx}. {format_atom(ch.atom)}", f"     原因类别: {groups}"]
    lines.append("     最小因果切片:")
    for e in ch.causal_slice:
        lines.append(f"       [{e.kind}] {e.obj}")
    status = "通过" if ch.replay_ok else "失败"
    lines.append(
        f"     重放验证: {status}（在 {base_name} 输入上{direction}切片即可重推出该结论）"
    )
    lines.append(f"     重放步骤: {', '.join(ch.replay_steps)}")
    return lines


def main():
    a, b = snapshot_t1(), snapshot_t2()
    report = diff_snapshots(a, b)
    out = [f"观察时刻 {a.name} -> {b.name} 的推导结论差异", "=" * 56, ""]

    out.append(f"【新增结论】（{len(report.gained)} 条）")
    for i, ch in enumerate(report.gained, 1):
        out += _fmt_change(i, ch, a.name, "仅应用")
    out.append("")

    out.append(f"【消失结论】（{len(report.lost)} 条）")
    for i, ch in enumerate(report.lost, 1):
        out += _fmt_change(i, ch, b.name, "恢复")
    out.append("")

    out.append(f"【结论不变但支持来源改变】（{len(report.support_changed)} 条，单列）")
    for sc in report.support_changed:
        out.append(f"  - {format_atom(sc.atom)}")
        out.append(f"      旧支持: {', '.join(format_key(k) for k in sc.old_support)}")
        out.append(f"      新支持: {', '.join(format_key(k) for k in sc.new_support)}")
        for rid, old_v, new_v in sc.rule_version_changes:
            out.append(f"      规则版本变更: {rid} v{old_v} -> v{new_v}（同 ID 内容变更）")
    out.append("")

    out.append(f"【未进入解释的无关输入】（{len(report.irrelevant_inputs)} 条）")
    for desc in report.irrelevant_inputs:
        out.append(f"  - {desc}")

    print("\n".join(out))


if __name__ == "__main__":
    main()
