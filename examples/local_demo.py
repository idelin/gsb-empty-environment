"""本地样例：展示异常段“后来才标记”导致的基线污染与回溯修订。

运行：
    python -m examples.local_demo
"""

from __future__ import annotations

from fractions import Fraction

from drift_revision import (
    DriftConfig,
    DriftDetector,
    DriftStatus,
    SampleKey,
    assert_replay_matches,
    replay_matches,
)


SOURCE = "sensor-a"
EVENTS = [
    (0, Fraction(10)),
    (1, Fraction(10)),
    (2, Fraction(1_000_000)),  # 极端值：最初进入后续基线
    (3, Fraction(30)),
    (4, Fraction(30)),
    (5, Fraction(30)),
]


def fmt_number(value: object) -> str:
    if value is None:
        return "-"
    if isinstance(value, Fraction) and value.denominator == 1:
        return str(value.numerator)
    return str(value)


def fmt_keys(keys: tuple[SampleKey, ...]) -> str:
    if not keys:
        return "-"
    return ",".join(str(key.seq) for key in keys)


def print_table(title: str, detector: DriftDetector) -> None:
    print(f"\n{title}")
    print("seq | time | value      | status       | n | mean       | variance | included | excluded")
    print("-" * 105)
    for decision in detector.decisions(SOURCE):
        print(
            f"{decision.seq:>3} | {decision.time:>4} | {fmt_number(decision.value):>10} | "
            f"{decision.status.value:<12} | {decision.baseline_n} | "
            f"{fmt_number(decision.mean):>10} | {fmt_number(decision.variance):>8} | "
            f"{fmt_keys(decision.included_keys):>8} | {fmt_keys(decision.excluded_keys)}"
        )


def print_revision_changes(detector: DriftDetector, report) -> None:
    print(f"\n本次操作修订 {len(report.revisions)} 个结果（新增 {report.new_decisions}，更新 {report.updated_decisions}）")
    for revision in report.revisions:
        before = f"v{revision.before_version}" if revision.before_version is not None else "无"
        print(f"- seq={revision.seq}: {before} -> v{revision.after_version}；{revision.cause}")
        for change in revision.changes:
            if change.field == "created":
                continue
            print(f"    {change.field}: {change.before!r} -> {change.after!r}")
    print(f"增量状态与逐步全量重放一致: {replay_matches(detector)}")


def detector_equivalent(detector: DriftDetector, reference: DriftDetector) -> bool:
    for seq in range(len(EVENTS)):
        actual = detector.decision(SOURCE, seq)
        expected = reference.decision(SOURCE, seq)
        comparable = (
            "status",
            "baseline_n",
            "mean",
            "variance",
            "included_keys",
            "excluded_keys",
        )
        for field in comparable:
            if getattr(actual, field) != getattr(expected, field):
                return False
    return True


def main() -> None:
    config = DriftConfig(lookback=5, min_baseline=2, threshold=3)
    detector = DriftDetector(config)

    print("== 1. 极端值先正常进入计算 ==")
    for time, value in EVENTS:
        detector.add_sample(SOURCE, time, time, value)
    print_table("补录前：seq=2 污染 t=3..5 的滚动基线", detector)
    contaminated = detector.decision(SOURCE, 3)
    print(
        "\n污染证据：seq=3 的基线包含 seq=2；mean="
        f"{contaminated.mean}，判定 {contaminated.status.value}。"
    )

    print("\n== 2. 后来补录异常段 [2,2]，沿实际依赖回溯传播 ==")
    report = detector.mark_anomaly(SOURCE, "late-extreme", 2, 2, note="事后确认传感器冲击")
    print_revision_changes(detector, report)
    print_table("补录后：seq=2 排除，后续基线从旧版本修订", detector)
    corrected = detector.decision(SOURCE, 3)
    print(
        "\n纠正后：seq=3 的 included 仅为 seq=0,seq=1；mean="
        f"{corrected.mean}，判定 {corrected.status.value}。"
    )

    reference = DriftDetector(config)
    reference.mark_anomaly(SOURCE, "late-extreme", 2, 2, note="假设从一开始就存在")
    for time, value in EVENTS:
        reference.add_sample(SOURCE, time, time, value)
    print(f"\n补录后的当前结果等价于“极端值从未纳入”：{detector_equivalent(detector, reference)}")

    print("\n== 3. 撤回异常段，值重新纳入并再次沿依赖传播 ==")
    withdrawn = detector.withdraw_anomaly(SOURCE, "late-extreme", note="复核后解除")
    print_revision_changes(detector, withdrawn)
    print_table("撤回后：seq=2 重新参与基线", detector)

    print("\n== 4. 每个受影响键保留修订链 ==")
    for seq in [2, 3, 4, 5]:
        chain = detector.decision_history(SOURCE, seq)
        statuses = " -> ".join(decision.status.value for decision in chain)
        versions = ", ".join(f"v{decision.version}" for decision in chain)
        print(f"- seq={seq}: {versions}；{statuses}")

    assert_replay_matches(detector)
    print("\n最终校验：每一步后的增量状态和完整修订链都与全量重放完全一致。")


if __name__ == "__main__":
    main()
