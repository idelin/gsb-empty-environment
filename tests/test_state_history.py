"""StateHistory 测试。

三类断言贯穿始终：
1. 直接查询当前段（h.segments()）；
2. 从初始段出发、按顺序应用全部增量补丁得到的段；
3. 用当前存活事件调用参考实现 build_segments 全量重算。
三者必须一致。
"""

import random

import pytest

from rle_history import (
    Event,
    EventRef,
    Segment,
    SegmentPatch,
    StateHistory,
    apply_patch,
    build_segments,
)

R = EventRef


def make_ab_history() -> StateHistory:
    """题目场景：A@0、A@2、B@5 -> 两个段。"""
    h = StateHistory()
    h.insert(0, "A", "dev", 1)
    h.insert(2, "A", "dev", 2)
    h.insert(5, "B", "dev", 3)
    return h


def assert_consistent(h: StateHistory, shadow: list[Segment]) -> None:
    """三路径核对 + 段列表结构不变量 + 事件证据不丢失。"""
    segs = h.segments()
    assert segs == shadow, "补丁路径与直接查询不一致"
    assert segs == build_segments(h.events()), "与原始事件重算不一致"
    for prev, cur in zip(segs, segs[1:]):
        assert prev.end == cur.start, "段之间必须首尾相接"
        assert prev.state != cur.state, "相邻段状态必须不同（最大合并）"
    if segs:
        assert segs[-1].end is None, "最后一段必须开放到 +∞"
        assert segs[0].start == min(e.time for e in h.events())
    # 每条存活事件必须且仅在某个段里出现一次（sources 或 overridden）
    refs = [r for s in segs for r in (*s.sources, *s.overridden)]
    assert sorted(refs) == sorted(e.ref for e in h.events())


# ---------------------------------------------------------------------------
# 基本形态与题目主场景
# ---------------------------------------------------------------------------


def test_initial_two_segments():
    h = make_ab_history()
    assert h.segments() == [
        Segment("A", 0, 5, (R("dev", 1), R("dev", 2))),
        Segment("B", 5, None, (R("dev", 3),)),
    ]
    assert h.state_at(-1) is None
    assert h.state_at(0) == "A"
    assert h.state_at(4) == "A"
    assert h.state_at(5) == "B"
    assert h.state_at(10**9) == "B"


def test_insert_splits_segment_and_patch():
    h = make_ab_history()
    patch = h.insert(1, "B", "dev", 4)
    # 补丁：拆掉旧 A 段，换成 A/B/A 三段
    assert patch.removed == (Segment("A", 0, 5, (R("dev", 1), R("dev", 2))),)
    assert patch.added == (
        Segment("A", 0, 1, (R("dev", 1),)),
        Segment("B", 1, 2, (R("dev", 4),)),
        Segment("A", 2, 5, (R("dev", 2),)),
    )
    # 拆分后仍可回溯原事件
    assert h.provenance(0) == (R("dev", 1),)
    assert h.provenance(1) == (R("dev", 4),)
    assert h.provenance(3) == (R("dev", 2),)
    assert h.state_at(1) == "B"


def test_retract_restores_structure_and_keeps_evidence():
    h = make_ab_history()
    original = h.segments()
    h.insert(1, "B", "dev", 4)
    patch = h.retract("dev", 4)
    # 结构完全恢复
    assert h.segments() == original
    # 合并没有丢掉中间重复记录 A@2 的证据
    assert R("dev", 2) in h.segments()[0].sources
    assert h.provenance(3) == (R("dev", 1), R("dev", 2))
    # 补丁是三段换一段
    assert len(patch.removed) == 3
    assert patch.added == (Segment("A", 0, 5, (R("dev", 1), R("dev", 2))),)


def test_patch_path_equals_direct_path_and_rebuild():
    """从初始段应用全部补丁 == 直接查询 == 原始事件重算。"""
    h = make_ab_history()
    shadow = h.segments()  # 初始段快照
    ops = [
        lambda: h.insert(1, "B", "dev", 4),
        lambda: h.retract("dev", 4),
        lambda: h.insert(3, "C", "dev", 5),
        lambda: h.correct("dev", 5, state="A"),
        lambda: h.correct("dev", 1, time=8),
        lambda: h.retract("dev", 3),
        lambda: h.insert(0, "B", "dev", 6),
        lambda: h.retract("dev", 6),
        lambda: h.retract("dev", 1),
        lambda: h.retract("dev", 2),
    ]
    for op in ops:
        patch = op()
        shadow = apply_patch(shadow, patch)
        assert_consistent(h, shadow)


# ---------------------------------------------------------------------------
# 边界：首末事件撤回、单条记录、空流
# ---------------------------------------------------------------------------


def test_retract_first_event():
    h = make_ab_history()  # A@0 A@2 B@5
    h.retract("dev", 1)    # 撤回首事件 A@0
    assert h.segments() == [
        Segment("A", 2, 5, (R("dev", 2),)),
        Segment("B", 5, None, (R("dev", 3),)),
    ]
    assert h.state_at(0) is None  # 首事件之前（含被撤回后裸露的头部）无状态
    assert h.state_at(1) is None
    assert h.state_at(2) == "A"


def test_retract_last_event():
    h = make_ab_history()
    patch = h.retract("dev", 3)  # 撤回末事件 B@5
    assert h.segments() == [Segment("A", 0, None, (R("dev", 1), R("dev", 2)))]
    assert patch.removed == (
        Segment("A", 0, 5, (R("dev", 1), R("dev", 2))),
        Segment("B", 5, None, (R("dev", 3),)),
    )
    assert patch.added == (Segment("A", 0, None, (R("dev", 1), R("dev", 2))),)
    assert h.state_at(10**6) == "A"


def test_single_record_then_empty():
    h = StateHistory()
    patch = h.insert(3, "A", "dev", 1)
    assert patch.removed == ()
    assert patch.added == (Segment("A", 3, None, (R("dev", 1),)),)
    assert h.state_at(2) is None
    assert h.state_at(3) == "A"
    patch = h.retract("dev", 1)
    assert patch.added == ()
    assert patch.removed == (Segment("A", 3, None, (R("dev", 1),)),)
    assert h.segments() == []


def test_empty_stream():
    h = StateHistory()
    assert h.segments() == []
    assert h.events() == []
    assert h.state_at(0) is None
    assert h.segment_at(0) is None
    assert h.provenance(0) is None
    assert len(h) == 0
    # 空流上第一条插入的补丁：只增不删
    patch = h.insert(7, "Z", "src", 1)
    assert patch.removed == ()
    assert h.state_at(7) == "Z"


# ---------------------------------------------------------------------------
# 同时间冲突
# ---------------------------------------------------------------------------


def test_same_time_conflict_winner_and_overridden_evidence():
    h = StateHistory()
    h.insert(0, "A", "dev", 1)
    h.insert(0, "B", "dev", 2)  # 同刻冲突：seq 大者生效
    segs = h.segments()
    assert segs == [Segment("B", 0, None, (R("dev", 2),), (R("dev", 1),))]
    assert h.state_at(0) == "B"
    # 被覆盖的 A@0 仍保留证据、可回溯
    assert R("dev", 1) in segs[0].overridden


def test_same_time_conflict_retract_winner_reveals_loser():
    h = StateHistory()
    h.insert(0, "A", "dev", 1)
    h.insert(0, "B", "dev", 2)
    h.retract("dev", 2)  # 撤回生效者，被覆盖者重新生效
    assert h.segments() == [Segment("A", 0, None, (R("dev", 1),))]


def test_same_time_conflict_tie_break_by_source():
    h = StateHistory()
    h.insert(4, "A", "alpha", 1)
    h.insert(4, "B", "beta", 1)  # seq 相同，source 字典序大者生效
    assert h.state_at(4) == "B"
    seg = h.segment_at(4)
    assert seg.sources == (R("beta", 1),)
    assert seg.overridden == (R("alpha", 1),)


def test_same_time_same_state_both_kept_as_sources():
    h = StateHistory()
    h.insert(0, "A", "s1", 1)
    h.insert(0, "A", "s2", 1)  # 同刻同状态：两条都是来源证据
    assert h.segments() == [
        Segment("A", 0, None, (R("s1", 1), R("s2", 1)))
    ]


# ---------------------------------------------------------------------------
# 更正
# ---------------------------------------------------------------------------


def test_correct_state_merges_segments():
    h = make_ab_history()  # A@0 A@2 B@5
    patch = h.correct("dev", 3, state="A")  # B@5 改成 A@5
    assert h.segments() == [
        Segment("A", 0, None, (R("dev", 1), R("dev", 2), R("dev", 3)))
    ]
    assert len(patch.removed) == 2 and len(patch.added) == 1


def test_correct_time_moves_event():
    h = make_ab_history()
    h.correct("dev", 1, time=10)  # A@0 -> A@10
    assert h.segments() == [
        Segment("A", 2, 5, (R("dev", 2),)),
        Segment("B", 5, 10, (R("dev", 3),)),
        Segment("A", 10, None, (R("dev", 1),)),
    ]
    assert h.state_at(0) is None
    assert h.state_at(9) == "B"
    assert h.state_at(10) == "A"


def test_correct_noop_returns_empty_patch():
    h = make_ab_history()
    patch = h.correct("dev", 1, state="A", time=0)
    assert patch.is_empty
    assert not patch


# ---------------------------------------------------------------------------
# 错误处理
# ---------------------------------------------------------------------------


def test_duplicate_insert_raises():
    h = make_ab_history()
    with pytest.raises(ValueError):
        h.insert(9, "C", "dev", 1)


def test_retract_missing_raises():
    h = make_ab_history()
    with pytest.raises(KeyError):
        h.retract("dev", 99)


def test_correct_missing_raises():
    h = make_ab_history()
    with pytest.raises(KeyError):
        h.correct("ghost", 1, state="A")


def test_patch_consumer_divergence_detected():
    h = make_ab_history()
    patch = h.insert(1, "B", "dev", 4)
    with pytest.raises(ValueError):
        apply_patch([], patch)  # 空视图套不上针对旧段的补丁


# ---------------------------------------------------------------------------
# 随机化属性测试：三路径一致性
# ---------------------------------------------------------------------------


def test_randomized_three_way_consistency():
    rng = random.Random(20260921)
    h = StateHistory()
    shadow: list[Segment] = []
    live: list[EventRef] = []
    counters = {"s1": 0, "s2": 0, "s3": 0}
    states = ["A", "B", "C"]

    def do_insert():
        src = rng.choice(list(counters))
        counters[src] += 1
        ref = R(src, counters[src])
        patch = h.insert(rng.randint(0, 30), rng.choice(states), src, counters[src])
        live.append(ref)
        return patch

    def do_retract():
        ref = rng.choice(live)
        live.remove(ref)
        return h.retract(ref.source, ref.seq)

    def do_correct():
        ref = rng.choice(live)
        old = next(e for e in h.events() if e.ref == ref)
        new_state = rng.choice([s for s in states if s != old.state] + [old.state])
        new_time = rng.randint(0, 30)
        if new_state == old.state and new_time == old.time:
            new_time = (old.time + 1) % 31
        return h.correct(ref.source, ref.seq, state=new_state, time=new_time)

    for _ in range(600):
        choices = ["insert"] * 3
        if live:
            choices += ["retract", "correct", "correct"]
        patch = {"insert": do_insert, "retract": do_retract, "correct": do_correct}[
            rng.choice(choices)
        ]()
        assert isinstance(patch, SegmentPatch)
        shadow = apply_patch(shadow, patch)
        assert_consistent(h, shadow)
