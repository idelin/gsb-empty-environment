"""随机版本序列测试：索引实现 vs 不依赖索引的参考实现。

每个 seed 生成一段随机操作序列（录入/更正/撤销/补录/未来版本），
同时作用于 BitemporalStore 与 ReferenceStore，随后用大量随机
(valid_time, as_of) 查询逐点比对 query / candidates / snapshot，
最后验证 GC 不破坏 horizon 之后的查询。
"""

import random

from bitemp import INF, BitemporalStore, ReferenceStore

OBJECTS = ["alpha", "beta", "gamma"]


def _random_ops(seed: int, n_ops: int):
    """生成确定性的随机操作序列（先生成、后应用，保证两实现吃到同样输入）。"""
    rng = random.Random(seed)
    ops = []
    open_ids: list[str] = []
    tx_of: dict[str, int] = {}
    counter = 0
    for _ in range(n_ops):
        kind = rng.choices(["assert", "correct", "retract"], [7, 2, 1])[0]
        if kind == "assert" or not open_ids:
            rid = f"r{counter}"
            counter += 1
            vf = rng.randint(0, 100)
            vt = INF if rng.random() < 0.1 else vf + rng.randint(1, 20)
            at = rng.randint(0, 500)  # 允许补录（at 晚于/早于 valid）与未来版本
            ops.append(("assert", (rng.choice(OBJECTS), vf, vt,
                                   rng.randint(0, 10**6), at,
                                   rng.randint(0, 2), rid)))
            open_ids.append(rid)
            tx_of[rid] = at
        else:
            rid = rng.choice(open_ids)
            open_ids.remove(rid)
            at = rng.randint(tx_of[rid], 500)  # 关闭时刻不早于录入时刻
            if kind == "correct":
                new_rid = f"r{counter}"
                counter += 1
                ops.append(("correct", (rid, rng.randint(0, 10**6), at, new_rid)))
                open_ids.append(new_rid)
                tx_of[new_rid] = at
            else:
                ops.append(("retract", (rid, at)))
    return ops


def _apply(store, ops):
    for kind, args in ops:
        if kind == "assert":
            obj, vf, vt, payload, at, prio, rid = args
            store.assert_version(obj, vf, vt, payload, at=at, priority=prio,
                                 record_id=rid)
        elif kind == "correct":
            rid, payload, at, new_rid = args
            store.correct(rid, payload, at=at, new_record_id=new_rid)
        else:
            rid, at = args
            store.retract(rid, at=at)


def test_randomized_against_reference():
    for seed in range(30):
        ops = _random_ops(seed, n_ops=200)
        idx, ref = BitemporalStore(), ReferenceStore()
        _apply(idx, ops)
        _apply(ref, ops)

        rng = random.Random(10**6 + seed)
        for _ in range(800):
            obj = rng.choice(OBJECTS)
            t = rng.randint(0, 130)
            as_of = rng.randint(0, 550)
            assert idx.query(obj, t, as_of=as_of) == ref.query(
                obj, t, as_of=as_of
            ), f"seed={seed} obj={obj} t={t} as_of={as_of}"
            assert idx.candidates(obj, t, as_of=as_of) == ref.candidates(
                obj, t, as_of=as_of
            )
        for _ in range(100):
            obj = rng.choice(OBJECTS)
            as_of = rng.randint(0, 550)
            assert idx.snapshot(obj, as_of=as_of) == ref.snapshot(
                obj, as_of=as_of
            )


def test_gc_keeps_post_horizon_queries_identical():
    for seed in range(10):
        ops = _random_ops(1000 + seed, n_ops=200)
        idx, ref = BitemporalStore(), ReferenceStore()
        _apply(idx, ops)
        _apply(ref, ops)

        horizon = 300
        assert idx.gc(horizon) == ref.gc(horizon)

        rng = random.Random(2000 + seed)
        for _ in range(500):
            obj = rng.choice(OBJECTS)
            t = rng.randint(0, 130)
            as_of = rng.randint(horizon, 600)  # 只承诺 horizon 之后的查询
            assert idx.query(obj, t, as_of=as_of) == ref.query(
                obj, t, as_of=as_of
            )
            assert idx.snapshot(obj, as_of=as_of) == ref.snapshot(
                obj, as_of=as_of
            )
