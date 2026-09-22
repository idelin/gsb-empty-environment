"""差异测试 + 夹具断言。

用随机版本序列在同一批 (t, as_of) 查询网格上对比：
  * VersionIndex（二分索引）
  * ref_get/ref_timeline_as_of/ref_evolutions（朴素线性扫描）
并验证：
  * 索引结果与记录插入顺序无关（打乱追加顺序，批量加载）；
  * 回收只删除被已确认后继取代的记录，回收后 as_of>=horizon 查询不变；
  * 夹具检查点（A@25、B@35、撤销@45 等）。
"""

from __future__ import annotations

import random

from bitemporal import (
    INF,
    Op,
    Record,
    Status,
    VersionIndex,
    purge,
    reclaimable_rids,
    ref_evolutions,
    ref_get,
    ref_timeline_as_of,
)
from bitemporal.fixtures import SCENARIOS, all_records

# 随机序列的时间在 0..HORIZON 内；INF_PROBE 仅用于开放区间探针
HORIZON = 40
TIMES = list(range(HORIZON + 1)) + [0.5 * k for k in range(2 * HORIZON + 1)]
OBJECTS = ["o0", "o1", "o2"]


def _make_random_log(rng: random.Random, n_ops: int) -> tuple[list[Record], dict]:
    """生成一条带链（更正/撤销）与独立重叠的随机追加日志。"""
    records: list[Record] = []
    rid_seq = 0
    # 每个对象维护若干“链”：链上记录同区间，可追加后继；独立记录各起新链
    chains: dict[str, list[Record]] = {obj: [] for obj in OBJECTS}

    def new_rid() -> str:
        nonlocal rid_seq
        rid_seq += 1
        return f"r{rid_seq:04d}"

    for _ in range(n_ops):
        obj = rng.choice(OBJECTS)
        if chains[obj] and rng.random() < 0.55:
            # 在随机一条已有链上追加同区间后继（更正或撤销）
            parent = rng.choice(chains[obj])
            at = rng.randint(parent.recorded_at, HORIZON + 10)
            if rng.random() < 0.2:
                rec = Record(new_rid(), obj, parent.vstart, parent.vend,
                             at, Op.RETRACT, None, parent.rid)
            else:
                rec = Record(new_rid(), obj, parent.vstart, parent.vend,
                             at, Op.ASSERT, rng.randint(0, 999), parent.rid)
        else:
            # 新的独立记录：随机左闭右开区间（可能开放），可能与他人重叠
            a = rng.randint(0, HORIZON)
            if rng.random() < 0.25:
                b = INF
            else:
                b = rng.randint(a + 1, HORIZON + 1)
            at = rng.randint(0, HORIZON + 10)
            rec = Record(new_rid(), obj, a, b, at, Op.ASSERT,
                         rng.randint(0, 999))
        records.append(rec)
        # 把后继挂到它所在链：用“直接前驱”回溯到链根登记
        chains[obj].append(rec)
    meta = {"chains": chains}
    return records, meta


def _query_grid(log: list[Record], idx: VersionIndex) -> None:
    probes = sorted({t for t in TIMES if t <= HORIZON + 1})
    as_ofs = [-1, 0, 5, 10, 15, 20, 25, 30, 35, HORIZON + 10]
    for obj in OBJECTS:
        for t in probes:
            for as_of in as_ofs:
                got = idx.get(obj, t, as_of)
                want = ref_get(log, obj, t, as_of)
                assert (got.status, got.value, got.rid) == (
                    want.status, want.value, want.rid
                ), (obj, t, as_of, got, want)
        for as_of in as_ofs:
            assert _norm(idx.timeline_as_of(obj, as_of)) == _norm(
                ref_timeline_as_of(log, obj, as_of)
            ), (obj, as_of)
        for t in probes:
            a = [(w, (r.status, r.value, r.rid)) for w, r in idx.evolutions(obj, t)]
            b = [(w, (r.status, r.value, r.rid)) for w, r in ref_evolutions(log, obj, t)]
            assert a == b, (obj, t, a, b)


def _norm(segs):
    return [(s.vstart, s.vend, s.status, s.value, s.rid) for s in segs]


def test_random_sequences_against_reference():
    rng = random.Random(20260922)
    for trial in range(300):
        log, _ = _make_random_log(rng, rng.randint(1, 60))
        idx = VersionIndex()
        idx.append_many(log)
        _query_grid(log, idx)


def test_insert_order_independence():
    rng = random.Random(4242)
    for _ in range(40):
        log, _ = _make_random_log(rng, 40)
        baseline = VersionIndex()
        baseline.append_many(log)

        # 打乱顺序批量加载：后继可能先到，先按 recorded_at 做拓扑排序后再打乱
        order = _valid_append_order(log, rng)
        shuffled = VersionIndex()
        shuffled.append_many([log[i] for i in order])

        for obj in OBJECTS:
            for t in range(0, HORIZON + 1):
                for as_of in range(0, HORIZON + 10, 5):
                    a = baseline.get(obj, t, as_of)
                    b = shuffled.get(obj, t, as_of)
                    assert (a.status, a.value, a.rid) == (b.status, b.value, b.rid)


def _valid_append_order(log: list[Record], rng: random.Random) -> list[int]:
    """生成一个合法但随机的追加顺序（父先于子，其余随机）。"""
    by_id = {r.rid: i for i, r in enumerate(log)}
    chosen: set[int] = set()
    order: list[int] = []
    remaining = set(range(len(log)))
    while remaining:
        ready = [i for i in remaining
                 if log[i].replaces_id is None
                 or by_id[log[i].replaces_id] in chosen]
        pick = rng.choice(ready)
        order.append(pick)
        chosen.add(pick)
        remaining.remove(pick)
    return order


def test_purge_preserves_recent_queries():
    rng = random.Random(7)
    horizon = 30
    for _ in range(20):
        log, _ = _make_random_log(rng, 50)
        idx = VersionIndex()
        idx.append_many(log)
        victims = reclaimable_rids(idx, horizon)

        before = {}
        for obj in OBJECTS:
            for t in range(0, HORIZON + 1):
                for as_of in range(horizon, HORIZON + 11, 5):
                    res = idx.get(obj, t, as_of)
                    before[(obj, t, as_of)] = (res.status, res.value, res.rid)
            before[("timeline", obj)] = _norm(idx.timeline_as_of(obj, horizon))

        purge(idx, horizon)
        for rid in victims:
            assert rid not in idx
        for key, want in before.items():
            if key[0] == "timeline":
                continue
            obj, t, as_of = key
            res = idx.get(obj, t, as_of)
            assert (res.status, res.value, res.rid) == want
        for obj in OBJECTS:
            assert _norm(idx.timeline_as_of(obj, horizon)) == before[("timeline", obj)]


def test_future_version_predecessor_not_reclaimable():
    log = [
        Record("p1", "o", 0, INF, 10, Op.ASSERT, "old"),
        Record("p2", "o", 0, INF, 50, Op.ASSERT, "new", "p1"),
    ]
    idx = VersionIndex()
    idx.append_many(log)
    assert reclaimable_rids(idx, 30) == []
    assert reclaimable_rids(idx, 50) == ["p1"]


def test_fixture_checkpoints():
    def build(name):
        idx = VersionIndex()
        idx.append_many(SCENARIOS[name].records)
        return idx

    basic = build("basic")
    assert basic.get("sensor", 10, 25).value == "A"
    assert basic.get("sensor", 10, 35).value == "B"
    assert basic.get("sensor", 10, 15).status is Status.UNKNOWN

    retro = build("retroactive_retraction")
    assert retro.get("sensor", 10, 25).value == "A"
    assert retro.get("sensor", 10, 35).value == "B"
    assert retro.get("sensor", 10, 45).status is Status.RETRACTED

    adj = build("adjacent")
    assert adj.get("contract", 9, 12).value == "X"
    assert adj.get("contract", 10, 12).value == "Y"  # 左闭右开边界

    back = build("backfill")
    assert back.get("backfill", 5, 20).status is Status.UNKNOWN
    assert back.get("backfill", 5, 25).value == "late-A"

    fut = build("future_version")
    assert fut.get("plan", 0, 25).value == "old"
    assert fut.get("plan", 0, 55).value == "new"


def test_append_validation():
    import pytest
    idx = VersionIndex()
    idx.append(Record("a", "o", 0, 10, 1, Op.ASSERT, 1))
    idx.append(Record("dup", "o", 0, 10, 2, Op.ASSERT, 2))

    bad = [
        Record("dup", "o", 0, 10, 3, Op.ASSERT, 3),          # 重复 rid
        Record("x", "o", 10, 0, 3, Op.ASSERT, 3),            # 非法区间
        Record("x", "o", 0, 10, 3, Op.RETRACT, 3),           # 撤销带值
        Record("x", "o", 0, 10, 3, Op.ASSERT, None),         # 断言空值
        Record("x", "o", 0, 10, 3, Op.ASSERT, 3, "ghost"),   # 未知前驱
        Record("x", "o", 0, 5, 4, Op.ASSERT, 3, "a"),        # 区间不一致
    ]
    for rec in bad:
        with pytest.raises(Exception):
            idx.append(rec)
