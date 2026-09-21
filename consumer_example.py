"""消费修订、恢复完整网格的示例。

消费者只保存一份网格状态字典：先用 grid() 引导快照，
之后每次摄入/撤回只应用 Revision 中变化的网格点，
即可始终恢复出与重采样器一致的完整网格。
"""

from fractions import Fraction

from resampler import Resampler


class GridConsumer:
    def __init__(self, resampler: Resampler):
        self.state = {gp.t: gp for gp in resampler.grid()}  # 引导快照

    def apply(self, revision):
        for gp in revision.changed:
            self.state[gp.t] = gp

    def snapshot(self, resampler: Resampler):
        return [self.state[gp.t] for gp in resampler.grid()]


def show(title, rs):
    print(f"\n== {title} ==")
    for gp in rs.grid():
        val = str(gp.value) if gp.valid else f"未知({gp.reason.value})"
        src = ",".join(gp.source_ids) or "-"
        print(f"  t={str(gp.t):>3}  value={val:<14} src={src}")


def main():
    rs = Resampler(origin=0, step=1, count=7, mode="linear", max_gap=6)
    consumer = GridConsumer(rs)

    # 1) 初始摄入 (0,0)、(4,8)：网格 2 线性插值取 4
    consumer.apply(rs.ingest([("a", 0, 0), ("b", 4, 8)]))
    show("摄入 a(0,0), b(4,8)", rs)

    # 2) 迟到样本 (2,3)：产生修订，网格 1..3 被改写
    rev = rs.ingest([("c", 2, 3)])
    consumer.apply(rev)
    print(f"\n迟到样本 c(2,3) -> 修订 {sorted(str(g.t) for g in rev.changed)}")
    show("应用修订后", rs)

    # 3) 撤回右端样本 b：受影响输出转为未知
    rev = rs.retract(["b"])
    consumer.apply(rev)
    print(f"\n撤回 b -> 修订 {sorted(str(g.t) for g in rev.changed)}")
    show("撤回 b 后", rs)

    # 4) 消费者仅凭 快照+修订 恢复出的完整网格，与重采样器一致
    assert consumer.snapshot(rs) == rs.grid()
    print("\n消费者恢复的完整网格与重采样器完全一致 ✔")

    # 5) 封闭水位线 3：影响区间整体封闭的样本被释放
    released = rs.seal(3)
    print(f"seal(3) 释放历史样本: {released}（网格状态不变）")
    assert consumer.snapshot(rs) == rs.grid()


if __name__ == "__main__":
    main()
