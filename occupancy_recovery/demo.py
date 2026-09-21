"""端到端可运行样例：乱序配对 -> 增量索引更新 -> 峰值查询。

运行：

    python3 demo.py

样例覆盖：半开区间交界不重叠、乱序到达、重复 end、端点更正、撤回、
"更正把区间移出查询范围"、非法 end<=start 的原子拒绝，以及孤立记录单列。
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from occupancy import OccupancyTracker, Outcome, RecordStatus  # noqa: E402
from occupancy.brute_force import brute_force_peak  # noqa: E402


def show(result, label: str) -> None:
    print(f"  {label}: {result.outcome.value:<18} status={result.status.value}"
          f"{(' interval=' + str(result.interval)) if result.interval else ''}")


def show_peak(tracker: OccupancyTracker, lo, hi, label: str) -> None:
    result = tracker.peak(lo, hi)
    reference = brute_force_peak(tracker.active_intervals(), lo, hi)
    ranges = ", ".join(
        f"[{p.start},{p.end if p.end is not None else '∞'})@{p.level}"
        f"{{{','.join(p.evidence)}}}"
        for p in result.periods
    )
    print(f"  peak[{lo},{hi if hi is not None else '∞'}) = {result.peak}: {ranges}")
    assert result.peak == reference.peak
    assert _periods_equal(result, reference), "增量查询与逐端点扫描不一致"


def _periods_equal(a, b) -> bool:
    key = lambda p: (p.start, p.end, p.level, p.evidence)
    return [key(p) for p in a.periods] == [key(p) for p in b.periods]


def main() -> None:
    tracker = OccupancyTracker()
    LO, HI = 0, 10

    print("1) 基础区间 A=[0,5), B=[5,9)：半开区间交界处不重叠，峰值为 1")
    show(tracker.report_start("A", 0), "A start")
    show(tracker.report_end("A", 5), "A end")
    show(tracker.report_end("B", 9), "B end (先到)")   # end 先于 start
    show(tracker.report_start("B", 5), "B start (后到)")
    show_peak(tracker, LO, HI, "")

    print("2) 加入 C=[4,6)：峰值升为 2，证据为同时活跃的占用 ID")
    show(tracker.report_start("C", 4), "C start")
    show(tracker.report_end("C", 6), "C end")
    show_peak(tracker, LO, HI, "")

    print("3) 重复结束 B@9：明确判为 duplicate，状态与峰值不变")
    show(tracker.report_end("B", 9), "B end repeat")
    show_peak(tracker, LO, HI, "")

    print("4) 撤回 C：其贡献全部撤销，峰值恢复为 1")
    show(tracker.withdraw("C"), "withdraw C")
    show_peak(tracker, LO, HI, "")

    print("5) 端点更正：A 的 end 5 -> 0（变成 [0,0)，移出 [0,10) 覆盖）")
    print("   旧 [0,5) 贡献必须全部撤销；非法的零长度更正应被拒绝")
    show(tracker.report_end("A", 0), "A end 5->0")

    print("   改为合法更正 A end 5 -> 2，区间收缩到 [0,2)：[3,10) 中只剩 B")
    show(tracker.report_end("A", 2), "A end 5->2")
    show_peak(tracker, 3, HI, "A 已不在 [3,10)")
    show_peak(tracker, LO, HI, "全范围")

    print("6) 让一次更正把区间整体移出查询范围：B=[5,9) 的 start 5 -> 10，")
    print("   得到 [10,9)? 非法；合法路径是 start 5 -> 12 => [12,9) 也非法。")
    print("   改用 end 9 -> 12 后 start 5 -> 10，得到 [10,12)，整体在 [0,10) 之外")
    show(tracker.report_end("B", 12), "B end 9->12")
    show(tracker.report_start("B", 10), "B start 5->10")
    show_peak(tracker, LO, HI, "B 已移出")
    show_peak(tracker, 0, 12, "在 [0,12) 内 B 仍贡献 [10,12)")

    print("7) 无效 end 早于 start：D 先报 start=8，再报 end=3，必须被拒绝")
    show(tracker.report_start("D", 8), "D start")
    bad = tracker.report_end("D", 3)
    show(bad, "D end 3")
    assert bad.outcome is Outcome.REJECTED_INVALID
    assert tracker.record("D").status is RecordStatus.START_ONLY
    assert tracker.record("D").end is None

    print("   end 先到 + 之后非法 start 也不能破坏此前状态：E end=4 已孤立存在")
    show(tracker.report_end("E", 4), "E end (孤立)")
    bad2 = tracker.report_start("E", 9)  # 9 >= end 4
    show(bad2, "E start 9 (非法)")
    assert bad2.outcome is Outcome.REJECTED_INVALID
    assert tracker.record("E").status is RecordStatus.END_ONLY
    assert tracker.record("E").start is None

    print("8) 孤立记录单列（D 缺 end，E 缺合法 start），不参与并发、不产生负占用")
    for record in tracker.orphan_records():
        print(f"   orphan {record.occupancy_id}: {record.status.value} "
              f"start={record.start} end={record.end}")
    show_peak(tracker, LO, HI, "含孤立记录时")

    print("9) 补全 D 后峰值查询恢复正常")
    show(tracker.report_end("D", 9), "D end 9")
    show_peak(tracker, LO, 12, "补全 D")

    print("OK：增量结果与逐端点暴力扫描全程一致。")


if __name__ == "__main__":
    main()
