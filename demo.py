"""本地样例：极端值污染基线 -> 补录异常标记 -> 回溯纠正 -> 撤回复原。

运行: python3 demo.py
"""

from driftdet import DriftDetector, Judgment

VALUES = [
    ("10.0", "正常"), ("10.4", "正常"), ("9.6", "正常"), ("10.2", "正常"),
    ("9.8", "正常"), ("10.3", "正常"),
    ("25",   "极端值(传感器尖峰)"),
    ("10.1", "正常"),
    ("14.0", "真实漂移"),
    ("10.0", "正常"), ("10.1", "正常"),
]


def fmt(frac):
    if frac is None:
        return "-"
    if frac.denominator == 1:
        return str(frac.numerator)
    return f"{frac}≈{float(frac):.4f}"


def snapshot(d):
    return {s: d.result("A", s).current for s in range(1, len(VALUES) + 1)}


def table(d, before=None, title=""):
    print(f"\n== {title} ==")
    print(f"{'seq':>3} {'值':>6}  {'判定':<12} {'基线均值':<18} 备注")
    for seq, (v, note) in enumerate(VALUES, 1):
        cur = d.result("A", seq).current
        mark = ""
        if before is not None and before[seq].judgment != cur.judgment:
            mark = f"  <== 由 {before[seq].judgment.value} 修订"
        print(f"{seq:>3} {v:>6}  {cur.judgment.value:<12} "
              f"{fmt(cur.mean):<18} {note}{mark}")


def show_chain(d, seq):
    print(f"\nseq {seq} 的修订链:")
    for r in d.revision_chain("A", seq):
        print(f"  v{r.version}: {r.judgment.value:<12} 均值={fmt(r.mean):<18}"
              f" 原因: {r.reason} (事件={r.cause_event})")


def check_replay(d, step):
    bad = d.verify_against_replay()
    status = "一致" if not bad else f"不一致: {bad}"
    print(f"[校验] {step}: 增量结果 vs 全量重放 -> {status}")
    assert not bad


def main():
    d = DriftDetector(window=5, k=2, min_baseline=3)
    print("参数: window=5, k=2, min_baseline=3, 判据 |x-μ| > k·σ (精确分数运算)")

    for i, (v, _) in enumerate(VALUES, 1):
        d.add_sample("A", i, f"2026-09-21T10:{i:02d}:00Z", v)
    before = snapshot(d)
    table(d, title="初始判定(极端值 25 已进入滚动基线)")
    print("\n观察: seq 7 被判 DRIFT; 但受污染基线(均值被抬高、方差被放大)")
    print("      把 seq 9 的真实漂移 14.0 掩盖成了 OK —— 漏报。")
    check_replay(d, "初始摄入")

    input("\n-- 回车补录异常标记 M1: A[7..7] --")
    d.mark_anomaly("M1", "A", 7, 7, reason="确认为传感器尖峰")
    table(d, before=before, title="补录标记后(回溯纠正)")
    print("\n观察: seq 7 改为 EXCLUDED; seq 8-11 的基线被重算,")
    print("      seq 9 由 OK 修订为 DRIFT —— 漏报被纠正。")
    show_chain(d, 9)
    check_replay(d, "补录 M1 后")

    input("\n-- 回车撤回标记 M1(误标) --")
    d.retract_mark("M1", reason="复核后撤销")
    table(d, before=snapshot(d), title="撤回标记后(重新纳入)")
    print("\n观察: seq 7 重新纳入并被判 DRIFT, 后续基线回到含 25 的状态,")
    print("      与初始判定逐条一致(修订链继续增长而非覆盖)。")
    show_chain(d, 7)
    check_replay(d, "撤回 M1 后")


if __name__ == "__main__":
    main()
