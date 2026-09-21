"""Tests: required scenarios, boundary semantics, order independence,
randomized oracle comparison against the index-free reference, and GC."""

from __future__ import annotations

import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from bitemp import (  # noqa: E402
    INF,
    BitemporalIndex,
    Kind,
    Version,
    birth_dominated,
    gc,
    reference_query,
    reference_timeline,
    reference_value,
)
from bitemp.fixtures import full_fixture, prompt_scenario  # noqa: E402


# -- the required example ----------------------------------------------------

def test_prompt_example():
    idx = BitemporalIndex(prompt_scenario())
    assert idx.value_at("acct-1", 10, 25) == "A"   # 观察 25：仍是 A
    assert idx.value_at("acct-1", 10, 35) == "B"   # 观察 35：已是 B
    assert idx.query("acct-1", 10, 19) is None     # 录入之前不可见


def test_retroactive_retraction_keeps_old_queries():
    idx = BitemporalIndex(full_fixture())
    assert idx.value_at("acct-1", 10, 25) == "A"
    assert idx.value_at("acct-1", 10, 35) == "B"   # 不被最新值污染
    v = idx.query("acct-1", 10, 45)
    assert v is not None and v.is_tombstone        # 追溯撤销是证据
    assert idx.value_at("acct-1", 10, 45) is None  # 但值视图变为"无"


def test_multiple_corrections_of_same_record():
    idx = BitemporalIndex(full_fixture())
    assert idx.value_at("acct-2", 10, 21) == "A"
    assert idx.value_at("acct-2", 10, 23) == "A'"
    assert idx.value_at("acct-2", 10, 25) == "A''"
    assert idx.value_at("acct-2", 10, 10_000) == "A''"


def test_future_version_and_backfill():
    idx = BitemporalIndex(full_fixture())
    assert idx.query("acct-3", 150, 24) is None        # 尚未录入
    assert idx.value_at("acct-3", 150, 26) == "future"  # 未来业务时间可见
    assert idx.query("acct-3", 10, 26) is None         # 不影响其它业务时刻
    assert idx.query("acct-5", 6, 49) is None          # 补录前：不知道
    assert idx.value_at("acct-5", 6, 51) == "old"      # 补录后：知道


def test_left_closed_right_open_and_abutting():
    idx = BitemporalIndex(full_fixture())
    assert idx.value_at("acct-4", 0, 10) == "p"
    assert idx.value_at("acct-4", 9, 10) == "p"
    assert idx.value_at("acct-4", 10, 10) == "q"   # 边界属于后一段
    assert idx.value_at("acct-4", 19, 10) == "q"
    assert idx.query("acct-4", 20, 10) is None
    assert idx.query("acct-4", -1, 10) is None


def test_visibility_boundary_is_left_closed():
    # tx_from == 观察时刻 时记录已可见（左闭）
    idx = BitemporalIndex(prompt_scenario())
    assert idx.value_at("acct-1", 10, 20) == "A"
    assert idx.value_at("acct-1", 10, 30) == "B"


# -- order independence --------------------------------------------------------

def test_ingestion_order_does_not_matter():
    versions = full_fixture()
    rng = random.Random(7)
    shuffled = versions[:]
    rng.shuffle(shuffled)
    a, b = BitemporalIndex(versions), BitemporalIndex(shuffled)
    for obj in ("acct-1", "acct-2", "acct-3", "acct-4", "acct-5", "ghost"):
        for t in range(-2, 60):
            for tx in (0, 5, 19, 20, 21, 25, 30, 40, 45, 100):
                assert a.query(obj, t, tx) == b.query(obj, t, tx)


# -- randomized oracle: index vs index-free reference --------------------------

def _random_versions(rng: random.Random, n: int) -> list[Version]:
    objs = ["a", "b", "c"]
    seqs: dict[str, int] = {}
    out = []
    for _ in range(n):
        obj = rng.choice(objs)
        seqs[obj] = seqs.get(obj, 0) + 1
        vf = rng.randint(0, 50)
        vt = vf + rng.randint(1, 10) if rng.random() < 0.8 else INF
        tx = rng.randint(0, 100)
        kind = (
            Kind.RETRACT
            if rng.random() < 0.15
            else rng.choice([Kind.ASSERT, Kind.CORRECT])
        )
        value = None if kind is Kind.RETRACT else rng.randint(0, 10_000)
        out.append(Version(obj, vf, vt, tx, seqs[obj], kind, value))
    return out


def test_randomized_index_matches_reference():
    for seed in range(50):
        rng = random.Random(seed)
        versions = _random_versions(rng, 60)
        rng.shuffle(versions)  # 任意到达顺序
        idx = BitemporalIndex(versions)
        for _ in range(300):
            obj = rng.choice(["a", "b", "c", "ghost"])
            t = rng.randint(-5, 65)
            tx = rng.randint(-5, 110)
            assert idx.query(obj, t, tx) == reference_query(
                versions, obj, t, tx
            ), f"seed={seed} obj={obj} t={t} tx={tx}"
            assert idx.value_at(obj, t, tx) == reference_value(
                versions, obj, t, tx
            )


def test_randomized_timeline_matches_reference():
    for seed in range(50, 70):
        rng = random.Random(seed)
        versions = _random_versions(rng, 40)
        idx = BitemporalIndex(versions)
        for obj in ("a", "b", "c"):
            for tx in (0, 10, 33, 57, 80, 100, 150):
                got = [(s.valid_from, s.valid_to, s.version)
                       for s in idx.timeline(obj, tx)]
                want = reference_timeline(versions, obj, tx)
                assert got == want, f"seed={seed} obj={obj} tx={tx}"


# -- retention / reclamation -----------------------------------------------------

def test_birth_dominated_only_by_same_tx_higher_seq():
    versions = [
        Version("o", 0, 10, 10, 1, Kind.ASSERT, "x"),
        Version("o", 0, 10, 10, 2, Kind.CORRECT, "y"),  # 同事务覆盖 v1
        Version("o", 0, 10, 20, 3, Kind.CORRECT, "z"),  # 晚录入，不影响 v2
    ]
    dead = birth_dominated(versions)
    assert [v.value for v in dead] == ["x"]
    kept, reclaimed = gc(versions)
    assert {v.value for v in kept} == {"y", "z"}
    assert [v.value for v in reclaimed] == ["x"]
    idx_before, idx_after = BitemporalIndex(versions), BitemporalIndex(kept)
    for t in range(-1, 12):
        for tx in range(0, 31):
            assert idx_before.query("o", t, tx) == idx_after.query("o", t, tx)


def test_later_correction_alone_does_not_reclaim():
    versions = prompt_scenario()  # A@20 被 B@30 更正，但 A 在观察 25 仍可见
    assert birth_dominated(versions) == []


def test_horizon_compaction_preserves_late_queries():
    rng = random.Random(99)
    versions = _random_versions(rng, 80)
    horizon = 40
    kept, reclaimed = gc(versions, horizon=horizon)
    assert all(v.tx_from <= horizon for v in reclaimed)
    assert len(kept) < len(versions)
    for _ in range(500):
        obj = rng.choice(["a", "b", "c"])
        t = rng.randint(-5, 65)
        tx = rng.randint(horizon, 130)  # 前提：不再查询 horizon 之前
        got = BitemporalIndex(kept).query(obj, t, tx)
        want = reference_query(versions, obj, t, tx)
        # 可观察答案（类型与值）必须一致；快照版本的区间是解析分片，不比较
        got_v = None if got is None else (got.kind, got.value)
        want_v = None if want is None else (want.kind, want.value)
        assert got_v == want_v, f"obj={obj} t={t} tx={tx}"
