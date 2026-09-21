"""合成夹具：覆盖题目要求的全部场景。

时间均为逻辑时钟整数。场景一览：

- order-1  主场景：事件 10 在 20 录入为 A，30 更正为 B，40 被追溯撤销，
           50 又录入一条业务区间在未来 [100, 101) 的版本。
- meter-1  相接区间：[10, 20) 与 [20, 30)，验证左闭右开边界。
- acct-1   同一记录多次更正：v1 -> v2 -> v3 -> v4。
- claim-1  重叠区间的显式优先级：低优先级长区间 + 高优先级审计子区间。
"""

from __future__ import annotations

from .core import INF, BitemporalStore


def build_demo_store() -> BitemporalStore:
    s = BitemporalStore()

    # 主场景：发生于 10 的值，20 录入为 A，30 更正为 B
    s.assert_version("order-1", 10, 11, "A", at=20, record_id="r1")
    s.correct("r1", "B", at=30, new_record_id="r2")
    # 追溯撤销：40 起 B 也不再可信（历史查询不受影响）
    s.retract("r2", at=40)
    # 未来版本：50 录入，业务区间在未来
    s.assert_version("order-1", 100, 101, "F", at=50, record_id="r3")

    # 相接区间（同一录入时刻）
    s.assert_version("meter-1", 10, 20, "lo", at=5, record_id="m1")
    s.assert_version("meter-1", 20, 30, "hi", at=5, record_id="m2")

    # 同一记录多次更正
    s.assert_version("acct-1", 0, INF, "v1", at=10, record_id="a1")
    s.correct("a1", "v2", at=20, new_record_id="a2")
    s.correct("a2", "v3", at=30, new_record_id="a3")
    s.correct("a3", "v4", at=40, new_record_id="a4")

    # 重叠区间的显式优先级
    s.assert_version("claim-1", 10, 50, "raw-source", at=10, priority=1, record_id="c1")
    s.assert_version("claim-1", 20, 30, "audited", at=15, priority=5, record_id="c2")

    return s
