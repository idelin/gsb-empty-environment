import random
import unittest
from decimal import Decimal
from fractions import Fraction

from resampler import Mode, Reason, Resampler, Sample


def values(rs):
    return [gp.value for gp in rs.grid()]


def at(rs, t):
    (gp,) = rs.query(t, Fraction(t) + 1) if False else [g for g in rs.grid() if g.t == Fraction(t)]
    return gp


class LinearBasicTest(unittest.TestCase):
    def test_prompt_example(self):
        # 样本 (0,0)、(4,8)，线性模式，网格 2 取 4
        rs = Resampler(origin=0, step=1, count=5, mode="linear", max_gap=10)
        rs.ingest([("a", 0, 0), ("b", 4, 8)])
        self.assertEqual(values(rs), [Decimal(0), Decimal(2), Decimal(4), Decimal(6), Decimal(8)])
        gp = at(rs, 2)
        self.assertTrue(gp.valid)
        self.assertEqual(gp.value, Decimal(4))
        self.assertEqual(gp.source_ids, ("a", "b"))
        self.assertEqual(gp.mode, Mode.LINEAR)
        # 精确命中样本点的网格点只用一个源
        self.assertEqual(at(rs, 0).source_ids, ("a",))
        self.assertEqual(at(rs, 4).source_ids, ("b",))

    def test_breakpoint_blocks_crossing(self):
        # 2 处有断点：不能跨断点得出网格 2 的值
        rs = Resampler(origin=0, step=1, count=5, mode="linear", max_gap=10, breakpoints=[2])
        rs.ingest([("a", 0, 0), ("b", 4, 8)])
        gp2 = at(rs, 2)
        self.assertFalse(gp2.valid)
        self.assertIsNone(gp2.value)
        self.assertEqual(gp2.reason, Reason.BREAKPOINT)
        self.assertEqual(gp2.source_ids, ())
        # 断点右侧没有左Bracket，左侧没有右Bracket
        self.assertFalse(at(rs, 1).valid)
        self.assertFalse(at(rs, 3).valid)
        # 样本精确命中处不受影响
        self.assertEqual(at(rs, 0).value, Decimal(0))
        self.assertEqual(at(rs, 4).value, Decimal(8))

    def test_retract_right_endpoint_turns_unknown(self):
        rs = Resampler(origin=0, step=1, count=5, mode="linear", max_gap=10)
        rs.ingest([("a", 0, 0), ("b", 4, 8)])
        rev = rs.retract(["b"])
        changed = {gp.t: gp for gp in rev.changed}
        # 受影响输出 1..4 转为未知，0 不变
        self.assertEqual(set(changed), {Fraction(1), Fraction(2), Fraction(3), Fraction(4)})
        for t in (1, 2, 3, 4):
            self.assertFalse(changed[Fraction(t)].valid)
            self.assertIsNone(changed[Fraction(t)].value)
            self.assertEqual(changed[Fraction(t)].reason, Reason.NO_DATA)
        self.assertEqual(at(rs, 0).value, Decimal(0))
        # 撤回未知 id 是 no-op
        self.assertEqual(rs.retract(["nope"]).changed, ())


class StepModeTest(unittest.TestCase):
    def test_right_continuous(self):
        rs = Resampler(origin=0, step=1, count=8, mode="step", max_gap=2)
        rs.ingest([("a", 0, 0), ("b", 4, 8)])
        got = values(rs)
        # 右连续：取最近 t <= 网格点的样本；保持至多 max_gap
        self.assertEqual(got[0], Decimal(0))
        self.assertEqual(got[1], Decimal(0))
        self.assertEqual(got[2], Decimal(0))   # 2-0 <= max_gap
        self.assertIsNone(got[3])              # 3-0 > 2 -> 缺测
        self.assertEqual(at(rs, 3).reason, Reason.GAP)
        self.assertEqual(got[4], Decimal(8))
        self.assertEqual(got[5], Decimal(8))
        self.assertEqual(got[6], Decimal(8))   # 6-4 <= 2
        self.assertIsNone(got[7])              # 7-4 > 2
        self.assertEqual(at(rs, 2).source_ids, ("a",))

    def test_step_blocked_by_breakpoint(self):
        rs = Resampler(origin=0, step=1, count=6, mode="step", max_gap=10, breakpoints=[2])
        rs.ingest([("a", 0, 0), ("b", 4, 8)])
        self.assertEqual(at(rs, 0).value, Decimal(0))
        self.assertEqual(at(rs, 1).value, Decimal(0))
        self.assertEqual(at(rs, 2).reason, Reason.BREAKPOINT)
        self.assertEqual(at(rs, 3).reason, Reason.BREAKPOINT)
        self.assertEqual(at(rs, 4).value, Decimal(8))
        self.assertEqual(at(rs, 5).value, Decimal(8))


class GapLimitTest(unittest.TestCase):
    def test_linear_gap(self):
        rs = Resampler(origin=0, step=1, count=11, mode="linear", max_gap=5)
        rs.ingest([("a", 0, 0), ("b", 10, 100)])
        self.assertEqual(at(rs, 0).value, Decimal(0))
        self.assertEqual(at(rs, 10).value, Decimal(100))
        for t in range(1, 10):
            gp = at(rs, t)
            self.assertFalse(gp.valid)
            self.assertEqual(gp.reason, Reason.GAP)


class PrecisionTest(unittest.TestCase):
    def test_non_integer_period_and_exact_grid(self):
        rs = Resampler(origin=0, step=Fraction(1, 4), count=7, mode="linear", max_gap=1)
        rs.ingest([("a", 0, 0), ("b", "0.5", 1), ("c", 1, 2), ("d", "1.5", 3)])
        times = [gp.t for gp in rs.grid()]
        self.assertEqual(times, [Fraction(i, 4) for i in range(7)])  # 精确网格位置
        self.assertEqual(at(rs, Fraction(1, 4)).value, Decimal("0.5"))
        self.assertEqual(at(rs, Fraction(3, 4)).value, Decimal("1.5"))
        self.assertEqual(at(rs, Fraction(5, 4)).value, Decimal("2.5"))

    def test_float_step_is_exact(self):
        rs = Resampler(origin=0, step=0.1, count=4, mode="step", max_gap=10)
        self.assertEqual([gp.t for gp in rs.grid()],
                         [Fraction(0), Fraction(1, 10), Fraction(1, 5), Fraction(3, 10)])

    def test_negative_times(self):
        rs = Resampler(origin=-2, step=1, count=5, mode="linear", max_gap=5)
        rs.ingest([("a", -3, 0), ("b", -1, 2), ("c", 1, 4)])
        self.assertEqual(at(rs, -2).value, Decimal(1))
        self.assertEqual(at(rs, -1).value, Decimal(2))
        self.assertEqual(at(rs, 0).value, Decimal(3))
        self.assertEqual(at(rs, 1).value, Decimal(4))
        self.assertFalse(at(rs, 2).valid)  # 没有右Bracket

    def test_fixed_rounding_half_up(self):
        rs = Resampler(origin=0, step=1, count=3, mode="linear", max_gap=10)
        rs.ingest([("a", 0, 0), ("b", 3, 1)])
        self.assertEqual(at(rs, 1).value, Decimal("0.333333"))
        self.assertEqual(at(rs, 2).value, Decimal("0.666667"))

    def test_half_up_tie_goes_away_from_zero(self):
        # 0.0000005 精确中点：HALF_UP -> 0.000001（HALF_EVEN 会给 0）
        rs = Resampler(origin=0, step=1, count=2, mode="linear", max_gap=10)
        rs.ingest([("a", 0, 0), ("b", 2, Fraction(1, 1_000_000))])
        self.assertEqual(at(rs, 1).value, Decimal("0.000001"))


class DuplicateAndEndpointTest(unittest.TestCase):
    def test_same_time_multiple_samples(self):
        rs = Resampler(origin=0, step=1, count=3, mode="step", max_gap=10)
        rs.ingest([("a", 1, 0, 1), ("b", 1, 10, 2)])  # version 大者生效
        self.assertEqual(at(rs, 1).value, Decimal(10))
        self.assertEqual(at(rs, 1).source_ids, ("b",))
        # 撤回生效样本后，被遮蔽的旧样本顶上（可更正）
        rev = rs.retract(["b"])
        self.assertEqual(at(rs, 1).value, Decimal(0))
        self.assertEqual(at(rs, 1).source_ids, ("a",))
        self.assertIn(at(rs, 1), rev.changed)

    def test_grid_endpoints(self):
        rs = Resampler(origin=0, step=1, count=5, mode="linear", max_gap=10)
        rs.ingest([("a", 2, 5)])
        self.assertEqual(at(rs, 0).reason, Reason.NO_DATA)   # 首端点：左侧无样本
        self.assertEqual(at(rs, 1).reason, Reason.NO_DATA)
        self.assertEqual(at(rs, 2).value, Decimal(5))
        self.assertEqual(at(rs, 3).reason, Reason.NO_DATA)   # 末端点：右侧无样本
        self.assertEqual(at(rs, 4).reason, Reason.NO_DATA)

    def test_step_endpoints(self):
        rs = Resampler(origin=0, step=1, count=5, mode="step", max_gap=10)
        rs.ingest([("a", 2, 5)])
        self.assertFalse(at(rs, 0).valid)
        self.assertFalse(at(rs, 1).valid)
        self.assertEqual([gp.value for gp in rs.grid()[2:]], [Decimal(5)] * 3)


class RevisionTest(unittest.TestCase):
    def test_late_sample_revises(self):
        rs = Resampler(origin=0, step=1, count=5, mode="linear", max_gap=10)
        rs.ingest([("a", 0, 0), ("b", 4, 8)])
        rev = rs.ingest([("c", 2, 10)])  # 迟到样本
        changed = {gp.t: gp for gp in rev.changed}
        self.assertEqual(set(changed), {Fraction(1), Fraction(2), Fraction(3)})
        self.assertEqual(changed[Fraction(1)].value, Decimal(5))
        self.assertEqual(changed[Fraction(2)].value, Decimal(10))
        self.assertEqual(changed[Fraction(3)].value, Decimal(9))
        self.assertEqual(changed[Fraction(1)].source_ids, ("a", "c"))
        self.assertEqual(changed[Fraction(3)].source_ids, ("c", "b"))

    def test_revision_seq_monotonic(self):
        rs = Resampler(origin=0, step=1, count=3, mode="step", max_gap=10)
        r1 = rs.ingest([("a", 0, 1)])
        r2 = rs.ingest([("b", 1, 2)])
        r3 = rs.retract(["a"])
        self.assertLess(r1.seq, r2.seq)
        self.assertLess(r2.seq, r3.seq)


class SealTest(unittest.TestCase):
    def test_seal_releases_closed_samples(self):
        rs = Resampler(origin=0, step=1, count=8, mode="step", max_gap=10)
        rs.ingest([("a", 0, 1), ("b", 2, 2), ("c", 4, 3), ("d", 6, 4)])
        before = rs.grid()
        released = rs.seal(4)
        # next(0)=2<=4, next(2)=4<=4 -> a,b 释放；c,d 的影响区间未封闭，保留
        self.assertEqual(released, ["a", "b"])
        self.assertEqual(rs.grid(), before)  # 网格不变
        # 已封闭区间拒绝写入
        with self.assertRaises(ValueError):
            rs.ingest([("x", 3, 9)])
        # 释放后的 id 撤回为 no-op
        self.assertEqual(rs.retract(["a"]).changed, ())
        # 水位线之上仍可迟到
        rev = rs.ingest([("e", 5, 9)])
        self.assertEqual({gp.t for gp in rev.changed}, {Fraction(5)})
        self.assertEqual(at(rs, 5).value, Decimal(9))
        # 未封闭区间与全新计算一致
        fresh = Resampler(origin=0, step=1, count=8, mode="step", max_gap=10)
        fresh.ingest([("c", 4, 3), ("d", 6, 4), ("e", 5, 9)])
        self.assertEqual(rs.grid()[4:], fresh.grid()[4:])


class RandomChunkTest(unittest.TestCase):
    def test_random_chunks_match_whole(self):
        for seed in range(40):
            rng = random.Random(seed)
            origin = Fraction(rng.randint(-5, 2))
            step = Fraction(rng.randint(1, 3), rng.choice([1, 2, 4]))
            count = rng.randint(4, 25)
            mode = rng.choice(["step", "linear"])
            max_gap = Fraction(rng.randint(1, 8), rng.choice([1, 2]))
            breakpoints = sorted(rng.sample(range(-6, 10), rng.randint(0, 3)))
            n = rng.randint(0, 30)
            samples = []
            for k in range(n):
                t = Fraction(rng.randint(-8, 12), rng.choice([1, 2, 4]))
                v = Fraction(rng.randint(-50, 50), rng.choice([1, 2, 3, 7]))
                samples.append((f"s{k}", t, v, k))  # version 固定 -> 与摄入顺序无关
            retract_ids = [s[0] for s in samples if rng.random() < 0.25]

            def build():
                return Resampler(origin=origin, step=step, count=count, mode=mode,
                                 max_gap=max_gap, breakpoints=breakpoints)

            # 参考：整段一次摄入
            ref = build()
            ref.ingest(samples)
            ref.retract(retract_ids)

            # 随机分块摄入 + 随机分组撤回
            chunked = build()
            consumer = {gp.t: gp for gp in chunked.grid()}  # 引导快照

            def apply(rev):
                for gp in rev.changed:
                    consumer[gp.t] = gp

            order = samples[:]
            rng.shuffle(order)
            i = 0
            while i < len(order):
                j = min(len(order), i + rng.randint(1, 7))
                apply(chunked.ingest(order[i:j]))
                i = j
            rng.shuffle(retract_ids)
            i = 0
            while i < len(retract_ids):
                j = min(len(retract_ids), i + rng.randint(1, 5))
                apply(chunked.retract(retract_ids[i:j]))
                i = j

            with self.subTest(seed=seed):
                self.assertEqual(chunked.grid(), ref.grid())
                # 消费者仅凭 快照+修订 恢复出完整网格
                self.assertEqual([consumer[gp.t] for gp in ref.grid()], ref.grid())


if __name__ == "__main__":
    unittest.main()
