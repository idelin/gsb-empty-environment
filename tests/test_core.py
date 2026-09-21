"""核心语义测试：更正窗口、追溯撤销、未来版本、相接区间、多次更正、
优先级裁决、插入顺序无关性、快照与 GC。"""

import random

import pytest

from bitemp import INF, BitemporalStore
from bitemp.fixtures import build_demo_store


def test_correction_visibility_windows():
    s = BitemporalStore()
    s.assert_version("o", 10, 11, "A", at=20, record_id="r1")
    s.correct("r1", "B", at=30, new_record_id="r2")

    assert s.query("o", 10, as_of=15) is None  # 尚未录入
    assert s.query("o", 10, as_of=25).payload == "A"  # 观察 25 仍是 A
    assert s.query("o", 10, as_of=35).payload == "B"  # 观察 35 是 B
    # 边界：tx 区间左闭右开
    assert s.query("o", 10, as_of=20).payload == "A"
    assert s.query("o", 10, as_of=30).payload == "B"


def test_old_queries_not_polluted_after_retract():
    s = build_demo_store()
    # 40 追溯撤销之后：
    assert s.query("order-1", 10, as_of=45) is None
    # 旧观察时刻结果不变
    assert s.query("order-1", 10, as_of=25).payload == "A"
    assert s.query("order-1", 10, as_of=35).payload == "B"
    assert s.query("order-1", 10, as_of=15) is None


def test_future_version_invisible_until_recorded():
    s = build_demo_store()
    # 业务区间在未来 [100, 101)，50 才录入
    assert s.query("order-1", 100, as_of=45) is None
    assert s.query("order-1", 100, as_of=55).payload == "F"
    # 未来版本不干扰 t=10 的查询
    assert s.query("order-1", 10, as_of=55) is None  # 已被撤销，仍为空


def test_adjacent_intervals_left_closed_right_open():
    s = build_demo_store()
    assert s.query("meter-1", 19, as_of=10).payload == "lo"
    assert s.query("meter-1", 20, as_of=10).payload == "hi"  # 边界归属后一段
    assert s.query("meter-1", 29, as_of=10).payload == "hi"
    assert s.query("meter-1", 30, as_of=10) is None
    assert s.query("meter-1", 10, as_of=4) is None  # 录入前不可见


def test_multiple_corrections_of_same_record():
    s = build_demo_store()
    expected = {15: "v1", 25: "v2", 35: "v3", 45: "v4"}
    for as_of, payload in expected.items():
        assert s.query("acct-1", 5, as_of=as_of).payload == payload
    # 全部更正完成后重放旧查询，结果不变
    for as_of, payload in expected.items():
        assert s.query("acct-1", 5, as_of=as_of).payload == payload


def test_priority_overlap_resolution():
    s = build_demo_store()
    # [20,30) 内高优先级 audited 胜出，区间外回落到 raw-source
    assert s.query("claim-1", 25, as_of=20).payload == "audited"
    assert s.query("claim-1", 15, as_of=20).payload == "raw-source"
    assert s.query("claim-1", 40, as_of=20).payload == "raw-source"
    # c2 录入前（as_of=12）只能看到 raw-source
    assert s.query("claim-1", 25, as_of=12).payload == "raw-source"


def test_result_independent_of_insertion_order():
    # 同一批独立记录按不同顺序写入，查询结果必须一致
    def ops():
        return [
            ("assert", ("k1", 0, 10, "p", 3, "x1")),
            ("assert", ("k1", 0, 10, "q", 3, "x2")),  # 同优先级同区间
            ("assert", ("k1", 5, 15, "hi", 9, "x3")),
            ("assert", ("k2", 0, INF, "open", 0, "y1")),
            ("assert", ("k2", 40, 50, "seg", 0, "y2")),
        ]

    def build(order):
        s = BitemporalStore()
        for i in order:
            _, (obj, vf, vt, payload, prio, rid) = ops()[i]
            s.assert_version(obj, vf, vt, payload, at=10 + i, priority=prio,
                             record_id=rid)
        return s

    base = build(range(5))
    rng = random.Random(7)
    for _ in range(20):
        perm = list(range(5))
        rng.shuffle(perm)
        other = build(perm)
        for obj in ("k1", "k2"):
            for t in (0, 5, 9, 10, 14, 45, 60):
                for as_of in (5, 12, 30):
                    assert base.query(obj, t, as_of=as_of) == other.query(
                        obj, t, as_of=as_of
                    )


def test_snapshot_knowledge_axis():
    s = build_demo_store()
    snap25 = s.snapshot("order-1", as_of=25)
    assert [v.payload for v in snap25] == ["A"]
    snap35 = s.snapshot("order-1", as_of=35)
    assert [v.payload for v in snap35] == ["B"]
    snap55 = s.snapshot("order-1", as_of=55)
    assert [v.payload for v in snap55] == ["F"]  # 撤销后只剩未来版本


def test_history_keeps_closed_versions():
    s = build_demo_store()
    hist = s.history("order-1")
    assert [(v.record_id, v.tx_from, v.tx_to) for v in hist] == [
        ("r1", 20, 30),
        ("r2", 30, 40),
        ("r3", 50, INF),
    ]


def test_gc_preserves_queries_at_or_after_horizon():
    s = build_demo_store()
    horizon = 35
    before = {
        (obj, t, a): s.query(obj, t, as_of=a)
        for obj in s.objects()
        for t in (0, 5, 10, 20, 25, 40, 100)
        for a in (horizon, 40, 55, 1000)
    }
    removed = s.gc(horizon)
    assert removed >= 2  # r1(tx_to=30)、r2(tx_to=40)? r2 tx_to=40 > 35 保留
    for key, v in before.items():
        obj, t, a = key
        assert s.query(obj, t, as_of=a) == v
    # 当前版本永不回收
    assert any(v.tx_to == INF for v in s.history("acct-1"))


def test_gc_exact_boundary():
    s = BitemporalStore()
    s.assert_version("o", 0, 1, "A", at=10, record_id="g1")
    s.correct("g1", "B", at=20, new_record_id="g2")
    assert s.gc(20) == 1  # g1 的 tx_to == 20 <= horizon 被回收
    assert s.query("o", 0, as_of=20).payload == "B"
    assert s.query("o", 0, as_of=100).payload == "B"


def test_invalid_operations_raise():
    s = BitemporalStore()
    with pytest.raises(ValueError):
        s.assert_version("o", 10, 10, "x", at=1, record_id="e1")  # 空区间
    with pytest.raises(ValueError):
        s.assert_version("o", 10, 5, "x", at=1, record_id="e2")  # 反向区间
    s.assert_version("o", 0, 1, "x", at=5, record_id="e3")
    with pytest.raises(ValueError):
        s.assert_version("o", 0, 1, "y", at=6, record_id="e3")  # 重复 id
    with pytest.raises(KeyError):
        s.correct("nope", "z", at=6, new_record_id="e4")
    with pytest.raises(ValueError):
        s.correct("e3", "z", at=4, new_record_id="e5")  # 早于录入时刻
    s.retract("e3", at=7)
    with pytest.raises(KeyError):
        s.retract("e3", at=8)  # 已关闭
