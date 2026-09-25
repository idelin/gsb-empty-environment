"""分析结果的紧凑文本输出。"""

from __future__ import annotations

from .solver import Analysis


def render(analysis: Analysis) -> str:
    if not analysis.events:
        return "（空系统）"
    lines: list[str] = []
    header = "事件：" + ", ".join(analysis.events)
    lines.append(header)
    if not analysis.feasible:
        lines.append("结论：不可行（检测到矛盾环）")
        for cyc in analysis.cycles:
            kind = "含区间界" if cyc.due_to_bounds else "纯因果"
            lines.append(
                f"  矛盾环 {kind} 总间隔 {cyc.total_gap}: "
                + " → ".join(cyc.nodes) + " ↺"
            )
            for edge in cyc.edges:
                lines.append(f"      证据: {edge}")
        return "\n".join(lines)

    lines.append("确定顺序层（层内互不可比较，层间为必然先后）：")
    for depth, layer in enumerate(analysis.layers):
        lines.append(f"  L{depth}: {{{', '.join(layer)}}}")

    lines.append("必然先后（紧凑表示 = 传递归约）：")
    if analysis.relations:
        for r in analysis.relations:
            lines.append(
                f"  {r.earlier} ≺ {r.later}  (保证间隔 ≥ {r.gap})"
            )
            lines.append(f"      证据链: {r.evidence}")
    else:
        lines.append("  （无必然先后边）")

    lines.append("全部必然关系（传递闭包）：")
    for r in analysis.closure:
        lines.append(f"  {r.earlier} ≺ {r.later} (≥{r.gap})")

    lines.append("不可比较组（成对列出；不可比较不具传递性，非等价类）：")
    if analysis.incomparable:
        for a, b in analysis.incomparable:
            lines.append(f"  {a} ∥ {b}")
    else:
        lines.append("  （无）")

    lines.append(
        f"可行时间见证(tick): "
        + ", ".join(f"{e}={v}" for e, v in analysis.witness.assignment.items())
    )
    lines.append(
        f"枚举核验: 可行赋时 {analysis.enumerated_solutions} 个"
        + ("（达到上限截断）" if analysis.enumeration_truncated else "（已穷尽）")
        + ("，与闭包判定一致 ✓" if analysis.enum_matches_closure else "，与闭包不一致 ✗")
    )
    return "\n".join(lines)
