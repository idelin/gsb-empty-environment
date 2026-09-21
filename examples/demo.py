"""连贯样例：乱序配对 -> 峰值查询 -> 重复/更正/撤回 -> 证据回溯。

直接运行：python3 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from occupancy_peak import OccupancyTracker


def show(tracker, qs, qe):
    r = tracker.peak(qs, qe)
    print(f"  区间: {tracker.intervals()}  孤立: {tracker.orphans()}")
    print(f"  查询 [{qs}, {qe}) -> 峰值并发 = {r.peak}")
    for seg in r.segments:
        print(f"    峰值时段 [{seg.start}, {seg.end})  贡献占用: {sorted(seg.ids)}")
    return r


def main():
    t = OccupancyTracker()

    print("== 1. 配对：A 正常到达，B 的 end 先于 start 到达（乱序） ==")
    print(" ", t.record_start("A", 0))
    print(" ", t.record_end("A", 5))
    print(" ", t.record_end("B", 9))    # end 先到，成为孤立记录
    print(" ", t.record_start("B", 5))  # start 后到，配对完成
    show(t, 0, 10)
    print("  -> [0,5) 与 [5,9) 在半开区间下交界处不重叠，峰值为 1")

    print("\n== 2. 重复结束：C 的 end=6 到达两次，第二次幂等忽略 ==")
    print(" ", t.record_end("C", 6))
    print(" ", t.record_end("C", 6))
    print(" ", t.record_start("C", 4))
    show(t, 0, 10)
    print("  -> 加入 [4,6) 后峰值升为 2")

    print("\n== 3. 撤回 C：峰值恢复为 1 ==")
    print(" ", t.retract("C"))
    print(" ", t.retract("C"))  # 已不存在
    show(t, 0, 10)

    print("\n== 4. 无效 end：把 B 的 end 更正为 2（早于 start=5），被拒绝 ==")
    print(" ", t.record_end("B", 2))
    print("  B 仍为:", t.interval("B"))
    show(t, 0, 10)
    print("  -> 无效端点不破坏此前状态")

    print("\n== 5. 端点更正：把 B 从 [5,9) 移出查询范围（先移 end 再移 start） ==")
    print(" ", t.record_end("B", 15))
    print(" ", t.record_start("B", 12))
    r = show(t, 0, 10)
    assert all("B" not in seg.ids for seg in r.segments)
    print("  -> B 在 [5,9) 的旧贡献已全部撤销，窗口内只剩 A")
    show(t, 10, 20)
    print("  -> B 的新区间 [12,15) 在新位置正常生效")

    print("\n== 6. 孤立记录单列：D 只有 start，不产生任何占用 ==")
    print(" ", t.record_start("D", 7))
    show(t, 0, 10)
    print("  -> 孤立端点不补零、不产生零长/负长度虚假占用")


if __name__ == "__main__":
    main()
