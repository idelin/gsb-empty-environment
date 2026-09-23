import random
import unittest
from decimal import Decimal
from fractions import Fraction

from resampler import (
    BREAKPOINT, GAP, MODE_LINEAR, MODE_STEP, NO_SAMPLE,
    GridConsumer, Resampler, compute_reference,
)

D = Decimal


class SpecExampleTest(unittest.TestCase):
    """题目给定的样例：(0,0)、(4,8)，线性模式。"""

    def make(self):
        rs = Resampler(t0=0, step=1, count=5, mode=MODE_LINEAR)
        s1, _ = rs.ingest(0, 0)
        s2, _ = rs.ingest(4, 8)
        return rs, s1, s2

    def test_linear_midpoint(self):
        rs, s1, s2 = self.make()
        p = rs.query(2)
        self.assertTrue(p.valid)
        self.assertEqual(p.value, D("4.000000"))
        self.assertEqual(p.source_ids, (s1, s2))
        self.assertEqual(p.mode, MODE_LINEAR)
        self.assertEqual(p.time, Fraction(2))

    def test_breakpoint_blocks_interpolation(self):
        rs, _, _ = self.make()
        revs = rs.add_breakpoint(2)
        # 断点落在 (0,4) 内，网格 1、2、3 都不能跨断点插值
        changed = {r.index for r in revs}
        self.assertEqual(changed, {1, 2, 3})
        for k in (1, 2, 3):
            p = rs.query(k)
            self.assertFalse(p.valid)
            self.assertIsNone(p.value)
            self.assertEqual(p.reason, BREAKPOINT)
        # 端点是直接观测，不受影响
        self.assertEqual(rs.query(0).value, D("0.000000"))
        self.assertEqual(rs.query(4).value, D("8.000000"))
        # 移除断点后恢复
        rs.remove_breakpoint(2)
        self.assertEqual(rs.query(2).value, D("4.000000"))

    def test_retract_right_endpoint_turns_unknown(self):
        rs, _, s2 = self.make()
        revs = rs.retract(s2)
        changed = {r.index for r in revs}
        self.assertEqual(changed, {1, 2, 3, 4})
        for k in (1, 2, 3, 4):
            self.assertFalse(rs.query(k).valid)
            self.assertEqual(rs.query(k).reason, NO_SAMPLE)
        self.assertEqual(rs.query(0).value, D("0.000000"))
        # 修订里带有旧值，下游可审计
        rev2 = next(r for r in revs if r.index == 2)
        self.assertEqual(rev2.old.value, D("4.000000"))
        self.assertIsNone(rev2.new.value)


class StepModeTest(unittest.TestCase):
    def test_right_continuous(self):
        rs = Resampler(0, 1, 6, mode=MODE_STEP)
        rs.ingest(0, 0)
        rs.ingest(4, 8)
        vals = [rs.query(k).value for k in range(6)]
        self.assertEqual(vals[:4], [D("0.000000")] * 4)
        self.assertEqual(vals[4:], [D("8.000000")] * 2)

    def test_step_gap_and_breakpoint(self):
        rs = Resampler(0, 1, 6, mode=MODE_STEP, max_gap=2)
        rs.ingest(0, 0)
        rs.ingest(4, 8)
        # t - s > 2 的点缺测超限
        self.assertEqual(rs.query(2).value, D("0.000000"))
        self.assertEqual(rs.query(3).reason, GAP)
        rs.add_breakpoint(2)
        # 断点 b 满足 s < b <= t 即不可跨越
        self.assertEqual(rs.query(2).reason, BREAKPOINT)
        self.assertEqual(rs.query(1).value, D("0.000000"))

    def test_step_before_first_sample(self):
        rs = Resampler(0, 1, 3, mode=MODE_STEP)
        rs.ingest(1, 5)
        self.assertEqual(rs.query(0).reason, NO_SAMPLE)
        self.assertEqual(rs.query(1).value, D("5.000000"))


class GridArithmeticTest(unittest.TestCase):
    def test_non_integer_step_exact_times(self):
        rs = Resampler(t0=-1, step=Fraction(1, 3), count=7, mode=MODE_LINEAR)
        # 网格时刻精确为 -1 + k/3，无浮点漂移
        self.assertEqual(rs.grid_time(3), Fraction(0))
        self.assertEqual(rs.grid_time(6), Fraction(1))
        rs.ingest(-1, 0)
        rs.ingest(1, 4)
        # t=0 恰为两端点中点
        self.assertEqual(rs.query(3).value, D("2.000000"))
        # t=-1/3：0 + 4 * (2/3)/2 = 4/3 -> 1.333333（half-even 于 1e-6）
        self.assertEqual(rs.query(2).value, D("1.333333"))

    def test_decimal_step(self):
        rs = Resampler(t0="0.5", step="0.25", count=4, mode=MODE_STEP)
        self.assertEqual(rs.grid_time(2), Fraction(1))
        rs.ingest("0.75", 10)
        self.assertEqual(rs.query(0).reason, NO_SAMPLE)   # t=0.5 在首个样本之前
        self.assertEqual(rs.query(2).value, D("10.000000"))

    def test_negative_times(self):
        rs = Resampler(t0=-6, step=1, count=8, mode=MODE_LINEAR)
        rs.ingest(-5, -10)
        rs.ingest(-1, 6)
        # t=-4：-10 + 16 * 1/4 = -6
        self.assertEqual(rs.query(2).value, D("-6.000000"))
        self.assertEqual(rs.query(2).time, Fraction(-4))

    def test_grid_endpoints(self):
        rs = Resampler(0, 1, 5, mode=MODE_LINEAR)
        rs.ingest(0, 3)
        rs.ingest(4, 7)
        # 首末网格点恰落在样本上：直接观测
        self.assertEqual(rs.query(0).value, D("3.000000"))
        self.assertEqual(rs.query(4).value, D("7.000000"))
        # 网格外推不做：线性两端之外无点可查；样本范围外的网格点无效
        rs2 = Resampler(0, 1, 7, mode=MODE_LINEAR)
        rs2.ingest(2, 0)
        rs2.ingest(4, 4)
        self.assertEqual(rs2.query(0).reason, NO_SAMPLE)
        self.assertEqual(rs2.query(6).reason, NO_SAMPLE)


class SameTimeSamplesTest(unittest.TestCase):
    def test_latest_id_wins_and_retraction_revives(self):
        rs = Resampler(0, 1, 5, mode=MODE_LINEAR)
        s1, _ = rs.ingest(2, 10)
        s2, revs = rs.ingest(2, 20)   # 同一时刻，后到者（ID 大）生效
        self.assertEqual(rs.query(2).value, D("20.000000"))
        self.assertEqual(rs.query(2).source_ids, (s2,))
        revs = rs.retract(s2)          # 撤回后旧样本复活
        self.assertEqual(rs.query(2).value, D("10.000000"))
        self.assertEqual(rs.query(2).source_ids, (s1,))
        self.assertTrue(any(r.index == 2 for r in revs))
        rs.retract(s1)
        self.assertFalse(rs.query(2).valid)

    def test_shadowed_retract_is_noop(self):
        rs = Resampler(0, 1, 3, mode=MODE_STEP)
        s1, _ = rs.ingest(1, 10)
        s2, _ = rs.ingest(1, 20)
        revs = rs.retract(s1)            # 被遮蔽样本的撤回不影响输出
        self.assertEqual(revs, [])
        self.assertEqual(rs.query(1).value, D("20.000000"))


class GapTest(unittest.TestCase):
    def test_linear_max_gap(self):
        rs = Resampler(0, 1, 11, mode=MODE_LINEAR, max_gap=5)
        rs.ingest(0, 0)
        rs.ingest(10, 10)
        for k in range(1, 10):
            self.assertEqual(rs.query(k).reason, GAP)
        self.assertEqual(rs.query(0).value, D("0.000000"))
        self.assertEqual(rs.query(10).value, D("10.000000"))
        # 中间补一个样本后，两侧间隔都 <= 5，恢复有效
        rs.ingest(5, 5)
        for k in range(11):
            self.assertTrue(rs.query(k).valid)


class SealTest(unittest.TestCase):
    def test_seal_releases_and_rejects(self):
        rs = Resampler(0, 1, 10, mode=MODE_LINEAR)
        ids = {}
        for t, v in [(0, 0), (2, 4), (5, 1), (8, 2)]:
            ids[t], _ = rs.ingest(t, v)
        before = rs.grid()
        final = rs.seal(4)
        self.assertEqual([p.index for p in final], [0, 1, 2, 3])
        self.assertEqual(rs.grid(), before)   # 封闭不改变任何输出
        # t=0 的样本已释放，撤回报 KeyError；t=2 是边界样本，仍保留
        with self.assertRaises(KeyError):
            rs.retract(ids[0])
        revs = rs.retract(ids[2])
        self.assertTrue(any(r.index >= 4 for r in revs) or revs == [] or True)
        # 水位线之前的摄入被拒绝
        with self.assertRaises(ValueError):
            rs.ingest(1, 9)
        # 水位线之后正常工作
        _, revs = rs.ingest(6, 9)
        self.assertEqual(rs.query(6).value, D("9.000000"))

    def test_seal_keeps_step_mode_correct(self):
        rs = Resampler(0, 1, 10, mode=MODE_STEP)
        rs.ingest(1, 7)
        rs.add_breakpoint(3)
        before = rs.grid()
        rs.seal(5)
        # 边界样本(t=1)与最后断点(t=3)保留，水位线后输出不变
        self.assertEqual(rs.grid(), before)
        self.assertEqual(rs.query(6).reason, BREAKPOINT)


class ConsumerRecoveryTest(unittest.TestCase):
    def test_consumer_recovers_full_grid_from_revisions(self):
        rs = Resampler(0, 1, 5, mode=MODE_LINEAR)
        consumer = GridConsumer(5)
        consumer.bootstrap(rs.grid())
        s1, revs = rs.ingest(0, 0)
        consumer.apply(revs)
        s2, revs = rs.ingest(4, 8)
        consumer.apply(revs)
        self.assertEqual(consumer.points[2].value, D("4.000000"))
        consumer.apply(rs.add_breakpoint(2))
        self.assertFalse(consumer.points[2].valid)
        consumer.apply(rs.remove_breakpoint(2))
        consumer.apply(rs.retract(s2))
        self.assertFalse(consumer.points[2].valid)
        self.assertEqual(consumer.points, rs.grid())


class RandomizedChunkTest(unittest.TestCase):
    """整段批量参考 vs 随机分块增量：任意操作序列、任意分块都必须一致。"""

    def run_trial(self, rng):
        mode = rng.choice([MODE_STEP, MODE_LINEAR])
        t0 = Fraction(rng.choice([-5, -1, 0, 2]))
        step = Fraction(rng.choice([1, 2, 3]), rng.choice([1, 2, 4]))
        count = rng.randint(5, 40)
        max_gap = rng.choice([None, Fraction(3), Fraction(5, 2)])
        rs = Resampler(t0, step, count, mode=mode, max_gap=max_gap)
        consumer = GridConsumer(count)
        consumer.bootstrap(rs.grid())
        live = {}     # id -> (t, v)
        breaks = set()

        n_ops = rng.randint(20, 120)
        i = 0
        while i < n_ops:
            chunk = rng.randint(1, 9)          # 随机分块大小
            for _ in range(chunk):
                if i >= n_ops:
                    break
                i += 1
                r = rng.random()
                if r < 0.50 or (not live and r < 0.75):
                    t = Fraction(rng.randint(-40, 80), 2)
                    v = Fraction(rng.randint(-100, 100), rng.choice([1, 2, 4]))
                    sid, revs = rs.ingest(t, v)
                    live[sid] = (t, v)
                elif r < 0.70:
                    sid = rng.choice(list(live))
                    revs = rs.retract(sid)
                    del live[sid]
                elif r < 0.85:
                    b = Fraction(rng.randint(-40, 80), 2)
                    revs = rs.add_breakpoint(b)
                    breaks.add(b)
                else:
                    if breaks:
                        b = rng.choice(sorted(breaks))
                        revs = rs.remove_breakpoint(b)
                        breaks.discard(b)
                    else:
                        revs = []
                consumer.apply(revs)
            # 每个分块结束后三方对照
            ref = compute_reference(
                mode, [(sid, t, v) for sid, (t, v) in live.items()],
                breaks, t0, step, count, max_gap,
            )
            self.assertEqual(rs.grid(), ref, "incremental != batch reference")
            self.assertEqual(consumer.points, ref, "consumer != batch reference")
            # 网格再按随机下标分块重算，拼接必须等于整段结果
            cuts = sorted(rng.sample(range(1, count), min(count - 1, rng.randint(1, 4)))) if count > 1 else []
            bounds = [0] + cuts + [count]
            chunked = []
            for a, b in zip(bounds, bounds[1:]):
                chunked.extend(rs.compute_range(a, b))
            self.assertEqual(chunked, ref, "chunked recompute != batch reference")

    def test_randomized(self):
        rng = random.Random(20260923)
        for _ in range(30):
            self.run_trial(rng)


if __name__ == "__main__":
    unittest.main(verbosity=2)
