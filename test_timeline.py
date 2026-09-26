"""unittest 测试套件：边界、撤回、随机小数据逐样本对照。"""
import random
import unittest

import fixtures
from timeline import Block, Timeline, TIME_TOLERANCE

TOL = TIME_TOLERANCE


# ---------- 独立的逐样本参照实现（与被测代码路径无关） ----------
def ref_intervals(blocks, calibrations):
    out = {}
    for b in blocks:
        s = b.nominal_start + calibrations.get(b.block_id, 0.0)
        out[b.block_id] = (s, s + b.duration)
    return out


def ref_owned_regions(blocks, calibrations):
    """每块的拥有区间：自身区间减去高优先级块区间。"""
    intervals = ref_intervals(blocks, calibrations)
    prio = {b.block_id: (b.version, b.block_id) for b in blocks}
    owned = {}
    for blk in blocks:
        regions = [intervals[blk.block_id]]
        for o in blocks:
            if o.block_id == blk.block_id or prio[o.block_id] <= prio[blk.block_id]:
                continue
            ca, cb = intervals[o.block_id]
            nxt = []
            for a, b in regions:
                if cb <= a or ca >= b:
                    nxt.append((a, b))
                else:
                    if a < ca:
                        nxt.append((a, min(ca, b)))
                    if cb < b:
                        nxt.append((max(cb, a), b))
            regions = nxt
        owned[blk.block_id] = regions
    return owned


def ref_owned_samples(blocks, calibrations, t0, t1):
    """逐样本判定：样本中点落在本块拥有区间内，且样本区间与查询相交。"""
    intervals = ref_intervals(blocks, calibrations)
    owned_regions = ref_owned_regions(blocks, calibrations)
    owned = []
    for blk in blocks:
        base = intervals[blk.block_id][0]
        rate = blk.sample_rate
        regions = owned_regions[blk.block_id]
        for i in range(blk.count):
            u0 = base + i / rate
            u1 = base + (i + 1) / rate
            if u1 <= t0 + TOL or u0 >= t1 - TOL:
                continue
            mid = (u0 + u1) / 2.0
            if any(a - TOL <= mid < b - TOL for a, b in regions):
                owned.append((blk.block_id, i))
    return sorted(owned)


def ref_holes(blocks, calibrations, t0, t1):
    """参照空洞：查询区间减去归属样本区间的并集。"""
    intervals = ref_intervals(blocks, calibrations)
    owned = ref_owned_samples(blocks, calibrations, t0, t1)
    by_id = {b.block_id: b for b in blocks}
    covered = []
    for bid, i in owned:
        base = intervals[bid][0]
        rate = by_id[bid].sample_rate
        covered.append((max(t0, base + i / rate), min(t1, base + (i + 1) / rate)))
    covered.sort()
    holes = []
    cursor = t0
    for a, b in covered:
        if a > cursor + TOL:
            holes.append((cursor, a))
        cursor = max(cursor, b)
    if cursor < t1 - TOL:
        holes.append((cursor, t1))
    return holes


def build(blocks, calibrations=None):
    tl = Timeline()
    for b in blocks:
        tl.add_block(b)
    for bid, off in (calibrations or {}).items():
        tl.set_calibration(bid, off)
    return tl


def result_pairs(result):
    return sorted((s.block_id, i)
                  for s in result.segments
                  for i in range(s.sample_start, s.sample_end))


class RateSwitchTest(unittest.TestCase):
    def test_new_rate_applied_after_switch(self):
        tl = build(fixtures.rate_switch_blocks())
        # b16k 共 1600 样本 @16kHz => 0.1 s；若沿用旧 8kHz 换算会得到 0.2 s
        t0, t1 = tl.sample_to_time("b16k", 0, 1600)
        self.assertAlmostEqual(t0, 0.1, delta=TOL)
        self.assertAlmostEqual(t1, 0.2, delta=TOL)
        mid0, _ = tl.sample_to_time("b16k", 800)
        self.assertAlmostEqual(mid0, 0.15, delta=TOL)  # 旧率会得 0.2

    def test_read_across_switch(self):
        tl = build(fixtures.rate_switch_blocks())
        res = tl.read(0.05, 0.15)
        self.assertEqual([s.block_id for s in res.segments], ["a8k", "b16k"])
        self.assertEqual(res.holes, ())
        self.assertEqual(res.segments[0].sample_start, 400)
        self.assertEqual(res.segments[1].sample_end, 800)


class GapTest(unittest.TestCase):
    def test_gap_reported_as_hole(self):
        tl = build(fixtures.gap_blocks())
        self.assertEqual(len(tl.holes), 1)
        self.assertAlmostEqual(tl.holes[0].t_start, 0.1, delta=TOL)
        self.assertAlmostEqual(tl.holes[0].t_end, 0.125, delta=TOL)
        res = tl.read(0.0, 0.225)
        self.assertEqual(len(res.holes), 1)
        self.assertAlmostEqual(res.holes[0].t_start, 0.1, delta=TOL)
        self.assertAlmostEqual(res.holes[0].t_end, 0.125, delta=TOL)

    def test_read_outside_coverage_is_hole(self):
        tl = build(fixtures.gap_blocks())
        res = tl.read(5.0, 6.0)
        self.assertEqual(res.segments, ())
        self.assertEqual(len(res.holes), 1)
        self.assertAlmostEqual(res.holes[0].t_start, 5.0, delta=TOL)
        self.assertAlmostEqual(res.holes[0].t_end, 6.0, delta=TOL)


class OverlapTest(unittest.TestCase):
    def test_version_priority_disjoint_slices(self):
        tl = build(fixtures.overlap_blocks())
        slices = tl.slices
        self.assertEqual([s.block_id for s in slices], ["old", "new"])
        self.assertEqual((slices[0].sample_start, slices[0].sample_end), (0, 8))
        self.assertEqual((slices[1].sample_start, slices[1].sample_end), (0, 16))
        for prev, nxt in zip(slices, slices[1:]):
            self.assertLessEqual(prev.t_end, nxt.t_start + TOL)  # 不相交

    def test_same_time_overlap_version_wins(self):
        tl = build(fixtures.same_time_overlap_blocks())
        self.assertEqual([s.block_id for s in tl.slices], ["dup_hi"])
        res = tl.read(0.0, 2.0)
        self.assertEqual(result_pairs(res), [("dup_hi", i) for i in range(8)])

    def test_retraction_restores_masked_samples(self):
        blocks = fixtures.overlap_blocks()
        tl = build(blocks)
        calib = {}
        before = result_pairs(tl.read(0.0, 3.0))
        self.assertEqual(before, sorted(
            [("old", i) for i in range(8)]
            + [("new", i) for i in range(16)]))
        tl.remove_block("new")  # 撤回高版本块
        after = result_pairs(tl.read(0.0, 3.0))
        self.assertEqual(after, [("old", i) for i in range(16)])
        self.assertEqual(after, ref_owned_samples(blocks[:1], calib, 0.0, 3.0))


class BoundaryTest(unittest.TestCase):
    def test_exact_boundary_no_duplicate(self):
        blocks = [
            Block("p", 8.0, fixtures.ramp(8), nominal_start=0.0, version=1),
            Block("q", 8.0, fixtures.ramp(8, start=100.0), nominal_start=1.0,
                  version=1),
        ]
        tl = build(blocks)
        res = tl.read(0.5, 1.5)
        pairs = result_pairs(res)
        self.assertEqual(len(pairs), len(set(pairs)))  # 无重复样本
        self.assertEqual(pairs, [("p", i) for i in range(4, 8)]
                                + [("q", i) for i in range(4)])
        # 恰好落在边界 t=1.0 的样本归属后块
        res2 = tl.read(1.0, 1.25)
        self.assertEqual(result_pairs(res2), [("q", 0), ("q", 1)])

    def test_query_exact_block_span(self):
        tl = build(fixtures.rate_switch_blocks())
        res = tl.read(0.0, 0.1)  # 恰好第一块
        self.assertEqual(result_pairs(res), [("a8k", i) for i in range(800)])
        self.assertEqual(res.holes, ())


class BidirectionalTest(unittest.TestCase):
    def test_roundtrip(self):
        tl = build(fixtures.rate_switch_blocks())
        for bid, count in (("a8k", 800), ("b16k", 1600)):
            for i in (0, 1, count // 2, count - 1):
                t0, t1 = tl.sample_to_time(bid, i, i + 1)
                ev = tl.time_to_sample(bid, (t0 + t1) / 2.0)
                self.assertEqual(ev.index, i)
                self.assertAlmostEqual(ev.residue, 0.5, delta=1e-6)

    def test_rounding_evidence_at_boundary(self):
        tl = build(fixtures.rate_switch_blocks())
        t0, _ = tl.sample_to_time("a8k", 400)
        ev = tl.time_to_sample("a8k", t0)
        self.assertEqual(ev.index, 400)
        self.assertAlmostEqual(ev.residue, 0.0, delta=1e-6)
        self.assertAlmostEqual(ev.position, 400.0, delta=1e-6)


class CalibrationTest(unittest.TestCase):
    def test_atomic_update_and_cursor_stability(self):
        tl = build(fixtures.rate_switch_blocks())
        cursor = tl.cursor()
        old_gen = tl.generation
        old_read = tl.read(0.0, 0.2)
        old_map = tl.sample_to_time("b16k", 0)

        tl.set_calibration("b16k", 0.5)  # 新校准改变历史块映射

        self.assertIsNot(tl.generation, old_gen)  # 整体换入新代
        new_map = tl.sample_to_time("b16k", 0)
        self.assertAlmostEqual(new_map[0], old_map[0] + 0.5, delta=TOL)
        # 相关区间一致更新
        self.assertAlmostEqual(tl.slices[-1].t_start, 0.6, delta=TOL)
        # 旧游标稳定：读到的仍是旧映射
        self.assertEqual(cursor.read(0.0, 0.2), old_read)
        self.assertEqual(cursor.sample_to_time("b16k", 0), old_map)
        self.assertEqual(cursor.slices, old_gen.slices)

    def test_calibration_reorders_blocks(self):
        # 校准使后插入的块时间上更早：索引不得假设时间单调
        blocks = [
            Block("x", 8.0, fixtures.ramp(8), nominal_start=0.0, version=1),
            Block("y", 8.0, fixtures.ramp(8, start=100.0), nominal_start=10.0,
                  version=1),
        ]
        tl = build(blocks, {"y": -9.0})  # y 移到 [1, 2)
        self.assertEqual([s.block_id for s in tl.slices], ["x", "y"])
        res = tl.read(0.0, 2.0)
        self.assertEqual(result_pairs(res),
                         [("x", i) for i in range(8)]
                         + [("y", i) for i in range(8)])
        self.assertEqual(res.holes, ())


class CrossCheckTest(unittest.TestCase):
    """随机小数据：索引结果 vs 独立逐样本参照，覆盖全部边界与撤回。"""

    def test_random_configs(self):
        rng = random.Random(20260926)
        for trial in range(200):
            n_blocks = rng.randint(1, 4)
            blocks = []
            calib = {}
            for k in range(n_blocks):
                rate = rng.choice([2.0, 4.0, 8.0])
                count = rng.randint(1, 12)
                start = rng.randint(-8, 8) * 0.25
                blocks.append(Block(f"blk{k}", rate,
                                    tuple(float(i) for i in range(count)),
                                    nominal_start=start,
                                    version=rng.randint(0, 3)))
                if rng.random() < 0.5:
                    calib[f"blk{k}"] = rng.randint(-4, 4) * 0.125
            order = list(blocks)
            rng.shuffle(order)  # 非时间顺序插入
            tl = build(order, calib)
            # 切片互不相交
            for prev, nxt in zip(tl.slices, tl.slices[1:]):
                self.assertLessEqual(prev.t_end, nxt.t_start + TOL,
                                     f"trial {trial}")
            for _ in range(20):
                t0 = rng.randint(-16, 16) * 0.25
                t1 = t0 + rng.randint(0, 16) * 0.125
                res = tl.read(t0, t1)
                ctx = f"trial {trial} range [{t0}, {t1})"
                self.assertEqual(result_pairs(res),
                                 ref_owned_samples(blocks, calib, t0, t1), ctx)
                got_holes = [(h.t_start, h.t_end) for h in res.holes]
                want_holes = ref_holes(blocks, calib, t0, t1)
                self.assertEqual(len(got_holes), len(want_holes), ctx)
                for (ga, gb), (wa, wb) in zip(got_holes, want_holes):
                    self.assertAlmostEqual(ga, wa, delta=TOL, msg=ctx)
                    self.assertAlmostEqual(gb, wb, delta=TOL, msg=ctx)
            # 撤回一个块后重新对照
            if blocks:
                victim = rng.choice(blocks)
                tl.remove_block(victim.block_id)
                rest = [b for b in blocks if b.block_id != victim.block_id]
                res = tl.read(-4.0, 4.0)
                self.assertEqual(result_pairs(res),
                                 ref_owned_samples(rest, calib, -4.0, 4.0),
                                 f"trial {trial} after retract {victim.block_id}")


class FixtureDeclarationTest(unittest.TestCase):
    def test_latency_and_tolerance_declared(self):
        self.assertGreater(fixtures.DECLARED_LATENCY_S, 0.0)
        self.assertGreater(fixtures.TIME_TOLERANCE, 0.0)
        for b in fixtures.rate_switch_blocks():
            self.assertEqual(b.declared_latency, fixtures.DECLARED_LATENCY_S)


if __name__ == "__main__":
    unittest.main(verbosity=2)
