"""端到端示例：分块摄入 -> 修订流 -> 下游消费者恢复完整网格 -> 封闭释放。"""

from resampler import GridConsumer, MODE_LINEAR, Resampler


def show(tag, points):
    row = []
    for p in points:
        cell = str(p.value) if p.valid else f"?({p.reason})"
        row.append(f"t={p.time}:{cell}")
    print(f"{tag:<28} {' | '.join(row)}")


def main():
    # 网格：t = 0,1,2,3,4，线性模式
    rs = Resampler(t0=0, step=1, count=5, mode=MODE_LINEAR)
    consumer = GridConsumer(5)
    consumer.bootstrap(rs.grid())          # 初始快照：全部未知
    show("初始", consumer.points)

    # 第一批样本到达
    s1, revs = rs.ingest(0, 0)
    consumer.apply(revs)
    s2, revs = rs.ingest(4, 8)
    consumer.apply(revs)
    show("摄入 (0,0)、(4,8)", consumer.points)   # t=2 插值得 4

    # 业务告知 t=2 处有断点：禁止跨断点插值
    consumer.apply(rs.add_breakpoint(2))
    show("断点 @2", consumer.points)             # t=1,2,3 转为未知

    # 断点撤销（例如确认是误报）：插值恢复
    consumer.apply(rs.remove_breakpoint(2))
    show("移除断点", consumer.points)

    # 撤回右端样本：受影响输出转为未知
    consumer.apply(rs.retract(s2))
    show("撤回 (4,8)", consumer.points)

    # 迟到样本依然可以补回，且消费者始终与重采样器一致
    _, revs = rs.ingest(4, 8)
    consumer.apply(revs)
    assert consumer.points == rs.grid()
    show("迟到补回 (4,8)", consumer.points)

    # 封闭 t<3 的区间：归档最终值，释放历史样本
    final = rs.seal(3)
    print(f"\n封闭水位线 3，归档 {len(final)} 个最终网格点；"
          f"此后 t<3 的摄入/撤回将被拒绝。")
    try:
        rs.ingest(1, 99)
    except ValueError as e:
        print(f"摄入 t=1 被拒绝：{e}")


if __name__ == "__main__":
    main()
