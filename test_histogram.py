"""unittest 测试套件。

要点：
- 用与 constraints._solve 完全独立的暴力枚举验证分位数界限；
- 验证"合并两个直方图 == 直接处理合并样本"；
- 验证不同边界不可直接合并、统计不相容必须拒绝；
- 验证不把按比例重分桶估计当成精确贡献。
"""

import math
import random
import unittest
from fractions import Fraction

from histogram import (BoundaryMismatch, FixedHistogram, HistogramError,
                       IncompatibleHistograms, NEG_INF, POS_INF, parse_q,
                       rank_of)
from constraints import reconcile, union
from table import Batch, Column, ColumnType, histogram_from_batch


# ----------------------------------------------------------------------
# 独立暴力枚举（不依赖 constraints._solve 的实现）
# ----------------------------------------------------------------------
def brute_force_assignments(num_atoms, equalities):
    """枚举所有满足等式约束的非负整数计数向量。"""
    sols = []
    current = [0] * num_atoms

    def rec(k, rem):
        if k == num_atoms:
            if all(r == 0 for r in rem):
                sols.append(tuple(current))
            return
        ub = None
        for ci, (idxs, _t) in enumerate(equalities):
            if k in idxs:
                ub = rem[ci] if ub is None else min(ub, rem[ci])
        if ub is None:
            ub = sum(t for _i, t in equalities)
        for v in range(ub, -1, -1):
            current[k] = v
            rec(k + 1, [rem[ci] - (v if k in idxs else 0)
                        for ci, (idxs, _t) in enumerate(equalities)])

    rec(0, [t for _i, t in equalities])
    return sols


def atom_of_rank(assignment, r):
    cum = 0
    for k, cnt in enumerate(assignment):
        cum += cnt
        if cum >= r:
            return k
    raise AssertionError("秩超出总数")


class TestFixedHistogram(unittest.TestCase):
    def test_right_edge_goes_to_next_bucket(self):
        h = FixedHistogram.from_values([0, 5, 10, 15, 19], [0, 10, 20])
        # 0->桶0, 5->桶0, 10->桶1(右端进下一桶), 15->桶1, 19->桶1
        self.assertEqual(h.counts, [2, 3])
        self.assertEqual(h.underflow, 0)
        self.assertEqual(h.overflow, 0)

    def test_underflow_overflow(self):
        h = FixedHistogram.from_values([-100, -1, 0, 9, 10, 50], [0, 10])
        self.assertEqual(h.underflow, 2)
        self.assertEqual(h.counts, [2])
        self.assertEqual(h.overflow, 2)  # 10 与 50，右端 10 属于上溢
        self.assertEqual(h.total, 6)

    def test_null_and_nan_are_distinct(self):
        nan = float("nan")
        h = FixedHistogram.from_values([None, nan, None, 1.0, nan], [0, 10])
        self.assertEqual(h.nulls, 2)
        self.assertEqual(h.nans, 2)
        self.assertEqual(h.total, 1)  # null/NaN 不计入分位总体

    def test_invalid_boundaries_rejected(self):
        for bad in ([5], [5, 5], [10, 0], [0, float("nan")], [0, float("inf")],
                    [0, True]):
            with self.assertRaises((ValueError, TypeError)):
                FixedHistogram(bad)

    def test_invalid_values_rejected(self):
        h = FixedHistogram([0, 10])
        for bad in (True, "x", 1 + 2j):
            with self.assertRaises(TypeError):
                h.add(bad)

    def test_merge_equals_direct_processing(self):
        rng = random.Random(20260925)
        bounds = [0, 3, 7, 10]
        for _ in range(50):
            s1 = [rng.randrange(-2, 13) for _ in range(rng.randrange(0, 30))]
            s2 = [rng.randrange(-2, 13) for _ in range(rng.randrange(0, 30))]
            h1 = FixedHistogram.from_values(s1, bounds)
            h2 = FixedHistogram.from_values(s2, bounds)
            merged = h1.merge(h2)
            direct = FixedHistogram.from_values(s1 + s2, bounds)
            self.assertEqual(merged, direct)

    def test_merge_different_boundaries_rejected(self):
        h1 = FixedHistogram.from_values([1, 2], [0, 10])
        h2 = FixedHistogram.from_values([1, 2], [0, 5, 10])
        with self.assertRaises(BoundaryMismatch):
            h1.merge(h2)

    def test_quantile_interval_contains_true_value(self):
        rng = random.Random(7)
        bounds = [0, 4, 8, 12, 16]
        for _ in range(100):
            sample = [rng.randrange(-3, 20) for _ in range(rng.randrange(1, 40))]
            h = FixedHistogram.from_values(sample, bounds)
            s = sorted(sample)
            n = len(s)
            for q in (0, Fraction(1, 3), 0.5, 0.9, 1):
                qi = h.quantile(q)
                r = rank_of(parse_q(q), n)
                true_v = s[r - 1]
                self.assertEqual(qi.rank, r)
                self.assertLessEqual(qi.lo, true_v)
                self.assertLess(true_v, qi.hi)
                self.assertIn("误差", qi.explanation)

    def test_quantile_rank_edges(self):
        h = FixedHistogram.from_values([1, 2, 3], [0, 10])
        self.assertEqual(h.quantile(0).rank, 1)
        self.assertEqual(h.quantile(1).rank, 3)
        self.assertEqual(h.quantile(Fraction(1, 3)).rank, 1)
        self.assertEqual(h.quantile(Fraction(2, 3)).rank, 2)
        with self.assertRaises(ValueError):
            h.quantile(1.5)
        with self.assertRaises(HistogramError):
            FixedHistogram([0, 10]).quantile(0.5)

    def test_integer_exactness(self):
        big = 2 ** 70
        h = FixedHistogram.from_values([big, big + 1, big + 2], [big, big + 2])
        self.assertEqual(h.counts, [2])       # big, big+1
        self.assertEqual(h.overflow, 1)       # big+2 在右端，进上溢
        qi = h.quantile(Fraction(1, 2))
        self.assertEqual((qi.lo, qi.hi), (big, big + 2))
        self.assertIsInstance(qi.lo, int)     # 整数边界保持精确 int

    def test_q_parsing(self):
        self.assertEqual(parse_q(0.1), Fraction(1, 10))  # 十进制字面量解释
        self.assertEqual(parse_q("1/3"), Fraction(1, 3))
        with self.assertRaises(TypeError):
            parse_q(True)


class TestBatch(unittest.TestCase):
    def test_schema_validation(self):
        cols = [Column("x", ColumnType.INT), Column("y", ColumnType.FLOAT),
                Column("s", ColumnType.TEXT), Column("b", ColumnType.BOOL)]
        b = Batch(cols, [(1, 0.5, "a", True), (None, None, None, None)])
        self.assertEqual(len(b), 2)
        with self.assertRaises(TypeError):
            Batch(cols, [(1.5, 0.5, "a", True)])       # INT 列拒绝 float
        with self.assertRaises(TypeError):
            Batch(cols, [(True, 0.5, "a", True)])      # INT 列拒绝 bool
        with self.assertRaises(ValueError):
            Batch(cols, [(1, 0.5, "a")])               # 列数不符
        with self.assertRaises(ValueError):
            Batch([Column("x", ColumnType.INT), Column("x", ColumnType.INT)], [])

    def test_histogram_from_batch_null_vs_nan(self):
        nan = float("nan")
        cols = [Column("v", ColumnType.FLOAT)]
        b = Batch(cols, [(1.0,), (None,), (nan,), (2.5,), (None,)])
        h = histogram_from_batch(b, "v", [0.0, 5.0])
        self.assertEqual(h.counts, [2])
        self.assertEqual(h.nulls, 2)
        self.assertEqual(h.nans, 1)
        self.assertEqual(h.total, 2)

    def test_int_column_exact(self):
        cols = [Column("v", ColumnType.INT)]
        b = Batch(cols, [(10 ** 30,), (10 ** 30 + 1,)])
        h = histogram_from_batch(b, "v", [10 ** 30, 10 ** 30 + 2])
        self.assertEqual(h.counts, [2])

    def test_non_numeric_column_rejected(self):
        b = Batch([Column("s", ColumnType.TEXT)], [("a",)])
        with self.assertRaises(TypeError):
            histogram_from_batch(b, "s", [0, 1])


class TestReconcile(unittest.TestCase):
    def test_single_histogram_roundtrip(self):
        h = FixedHistogram.from_values([1, 2, 5, 8, 9], [0, 4, 10])
        ch = reconcile([h])
        qb = ch.quantile(0.5)
        qi = h.quantile(0.5)
        self.assertEqual((qb.lo, qb.hi), (qi.lo, qi.hi))

    def test_basic_reconcile_and_true_value_covered(self):
        sample = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
        h1 = FixedHistogram.from_values(sample, [0, 5, 10])
        h2 = FixedHistogram.from_values(sample, [0, 3, 7, 10])
        ch = reconcile([h1, h2])
        s = sorted(sample)
        for q in (0.1, 0.5, 0.9, 1):
            qb = ch.quantile(q)
            true_v = s[qb.rank - 1]
            self.assertLessEqual(qb.lo, true_v)
            self.assertLess(true_v, qb.hi)

    def test_incompatible_rejected(self):
        base = FixedHistogram.from_values([1, 2, 3], [0, 10])
        # 总数不一致
        other = FixedHistogram.from_values([1, 2, 3, 4], [0, 5, 10])
        with self.assertRaises(IncompatibleHistograms):
            reconcile([base, other])
        # null 数不一致
        h_null = FixedHistogram.from_values([1, 2, 3, None], [0, 5, 10])
        with self.assertRaises(IncompatibleHistograms):
            reconcile([base, h_null])
        # 总数一致但桶约束互相矛盾（无可行非负整数分布）
        h_a = FixedHistogram([0, 5, 10], counts=[3, 0])
        h_b = FixedHistogram([0, 7, 10], counts=[0, 3])
        with self.assertRaises(IncompatibleHistograms):
            reconcile([h_a, h_b])

    def test_witness_sample_reconstructs_sources(self):
        sample = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
        h1 = FixedHistogram.from_values(sample, [0, 5, 10])
        h2 = FixedHistogram.from_values(sample, [0, 3, 7, 10])
        ch = reconcile([h1, h2])
        qb = ch.quantile(0.5)
        for witness in (qb.low_witness, qb.high_witness):
            wsample = ch.expand_witness(witness)
            self.assertEqual(len(wsample), ch.total)
            for src in ch.sources:
                rebuilt = FixedHistogram.from_values(wsample, src.boundaries)
                self.assertEqual(rebuilt, src)  # 见证样本精确还原每个源

    def test_bounds_match_independent_enumeration(self):
        rng = random.Random(99)
        checked = 0
        for _ in range(200):
            nb1 = rng.randrange(2, 5)
            nb2 = rng.randrange(2, 5)
            cuts = sorted(rng.sample(range(1, 12), nb1 - 1))
            b1 = [0] + cuts + [12]
            cuts2 = sorted(rng.sample(range(1, 12), nb2 - 1))
            b2 = [0] + cuts2 + [12]
            sample = [rng.randrange(0, 12) for _ in range(rng.randrange(1, 9))]
            h1 = FixedHistogram.from_values(sample, b1)
            h2 = FixedHistogram.from_values(sample, b2)
            ch = reconcile([h1, h2])
            sols = brute_force_assignments(len(ch.atoms), ch.equalities)
            self.assertTrue(sols)
            s = sorted(sample)
            for q in (Fraction(1, 4), Fraction(1, 2), Fraction(3, 4), Fraction(1)):
                qb = ch.quantile(q)
                hit = {atom_of_rank(a, qb.rank) for a in sols}
                # 界限与独立枚举的可达原子集合一致
                self.assertEqual(qb.lo, ch.atoms[min(hit)].lo)
                self.assertEqual(qb.hi, ch.atoms[max(hit)].hi)
                self.assertEqual(qb.lo_atom, min(hit))
                self.assertEqual(qb.hi_atom, max(hit))
                # 见证是真实相容分布且达到端点原子
                self.assertIn(qb.low_witness, sols)
                self.assertIn(qb.high_witness, sols)
                self.assertEqual(atom_of_rank(qb.low_witness, qb.rank), qb.lo_atom)
                self.assertEqual(atom_of_rank(qb.high_witness, qb.rank), qb.hi_atom)
                # 真实分位数被界限覆盖
                true_v = s[qb.rank - 1]
                self.assertLessEqual(qb.lo, true_v)
                self.assertLess(true_v, qb.hi)
            checked += 1
        self.assertGreater(checked, 100)

    def test_underflow_overflow_atoms_in_bounds(self):
        sample = [-5, -1, 0, 3, 9, 20]
        h1 = FixedHistogram.from_values(sample, [0, 5, 10])
        h2 = FixedHistogram.from_values(sample, [0, 2, 8, 10])
        ch = reconcile([h1, h2])
        qb_lo = ch.quantile(0)      # rank=1，落在下溢原子
        self.assertEqual(qb_lo.lo, NEG_INF)
        self.assertEqual(qb_lo.hi, 0)
        qb_hi = ch.quantile(1)      # rank=n，落在上溢原子
        self.assertEqual(qb_hi.lo, 10)
        self.assertEqual(qb_hi.hi, POS_INF)

    def test_quantile_empty_rejected(self):
        h = FixedHistogram([0, 10])
        ch = reconcile([h])
        with self.assertRaises(HistogramError):
            ch.quantile(0.5)


class TestUnion(unittest.TestCase):
    def test_no_proportional_rebucketing(self):
        # 并集：shard1=[0,10,20]:[6,4]，shard2=[0,5,15,20]:[2,6,2]。
        # 若把 shard1 按桶宽比例重分桶到原子，会估计原子计数
        # [3,3,2,2]+[2,3,3,2]=[5,6,5,4]，从而断言中位数落在 [5,10)。
        # 但相容拆分允许 rank=10 落到 [10,15)（如 y1=1），
        # 比例估计不是精确贡献，不能当成精确值。
        s1 = FixedHistogram([0, 10, 20], counts=[6, 4])
        s2 = FixedHistogram([0, 5, 15, 20], counts=[2, 6, 2])
        ch = union([s1, s2])
        qb = ch.quantile(0.5)  # rank=10, n=20
        self.assertEqual((qb.lo, qb.hi), (5, 15))
        self.assertLess(qb.lo, qb.hi)  # 诚实的区间，而非点估计
        self.assertIn("未做桶内均匀假定", qb.explanation)

    def test_union_single_source_matches_histogram(self):
        h = FixedHistogram.from_values([1, 2, 5, 8, 9], [0, 4, 10])
        ch = union([h])
        qb = ch.quantile(0.5)
        qi = h.quantile(0.5)
        self.assertEqual((qb.lo, qb.hi), (qi.lo, qi.hi))

    def test_union_covers_true_quantile(self):
        rng = random.Random(4242)
        for _ in range(60):
            b1 = [0] + sorted(rng.sample(range(1, 12), rng.randrange(1, 4))) + [12]
            b2 = [0] + sorted(rng.sample(range(1, 12), rng.randrange(1, 4))) + [12]
            s1 = [rng.randrange(0, 12) for _ in range(rng.randrange(1, 10))]
            s2 = [rng.randrange(0, 12) for _ in range(rng.randrange(1, 10))]
            ch = union([FixedHistogram.from_values(s1, b1),
                        FixedHistogram.from_values(s2, b2)])
            s = sorted(s1 + s2)
            for q in (0.25, 0.5, 0.75, 1):
                qb = ch.quantile(q)
                true_v = s[qb.rank - 1]
                self.assertLessEqual(qb.lo, true_v)
                self.assertLess(true_v, qb.hi)

    def test_union_witness_sample_matches_atoms(self):
        s1 = FixedHistogram([0, 10, 20], counts=[6, 4])
        s2 = FixedHistogram([0, 5, 15, 20], counts=[2, 6, 2])
        ch = union([s1, s2])
        qb = ch.quantile(0.5)
        edges = sorted({a.lo for a in ch.atoms if a.lo != NEG_INF}
                      | {a.hi for a in ch.atoms if a.hi != POS_INF})
        for witness in (qb.low_witness, qb.high_witness):
            # 见证变量满足每个源的桶约束（独立枚举已验证其属于解集）
            self.assertIn(witness, brute_force_assignments(
                len(ch.var_atom), ch.equalities))
            for strategy in ("left", "right"):
                sample = ch.expand_witness(witness, strategy)
                self.assertEqual(len(sample), ch.total)
                # 展开样本在融合边界上的原子计数与见证完全一致
                h = FixedHistogram.from_values(sample, edges)
                got = [h.underflow] + h.counts + [h.overflow]
                self.assertEqual(got, ch.atom_totals(witness))

    def test_union_witness_sample_achieves_endpoints(self):
        s1 = FixedHistogram([0, 10, 20], counts=[6, 4])
        s2 = FixedHistogram([0, 5, 15, 20], counts=[2, 6, 2])
        ch = union([s1, s2])
        qb = ch.quantile(0.5)
        lo_sample = ch.expand_witness(qb.low_witness, "left")
        hi_sample = ch.expand_witness(qb.high_witness, "right")
        # 左端点可由相容样本精确达到；右端点为开区间，整数域上达到 hi-1
        self.assertEqual(sorted(lo_sample)[qb.rank - 1], qb.lo)
        self.assertEqual(sorted(hi_sample)[qb.rank - 1], qb.hi - 1)

    def test_union_bounds_match_independent_enumeration(self):
        rng = random.Random(31337)
        checked = 0
        for _ in range(120):
            b1 = [0] + sorted(rng.sample(range(1, 10), rng.randrange(1, 4))) + [10]
            b2 = [0] + sorted(rng.sample(range(1, 10), rng.randrange(1, 4))) + [10]
            s1 = [rng.randrange(0, 10) for _ in range(rng.randrange(1, 7))]
            s2 = [rng.randrange(0, 10) for _ in range(rng.randrange(1, 7))]
            h1 = FixedHistogram.from_values(s1, b1)
            h2 = FixedHistogram.from_values(s2, b2)
            ch = union([h1, h2])
            sols = brute_force_assignments(len(ch.var_atom), ch.equalities)
            self.assertTrue(sols)
            s = sorted(s1 + s2)
            for q in (Fraction(1, 4), Fraction(1, 2), Fraction(1)):
                qb = ch.quantile(q)
                hit = set()
                for a in sols:
                    hit.add(atom_of_rank(ch.atom_totals(a), qb.rank))
                self.assertEqual(qb.lo, ch.atoms[min(hit)].lo)
                self.assertEqual(qb.hi, ch.atoms[max(hit)].hi)
                self.assertEqual((qb.lo_atom, qb.hi_atom), (min(hit), max(hit)))
                self.assertIn(qb.low_witness, sols)
                self.assertIn(qb.high_witness, sols)
                self.assertEqual(
                    atom_of_rank(ch.atom_totals(qb.low_witness), qb.rank),
                    qb.lo_atom)
                self.assertEqual(
                    atom_of_rank(ch.atom_totals(qb.high_witness), qb.rank),
                    qb.hi_atom)
                true_v = s[qb.rank - 1]
                self.assertLessEqual(qb.lo, true_v)
                self.assertLess(true_v, qb.hi)
            checked += 1
        self.assertGreater(checked, 60)

    def test_union_rejects_non_histogram(self):
        with self.assertRaises(TypeError):
            union([FixedHistogram([0, 1]), "x"])
        with self.assertRaises(ValueError):
            union([])


if __name__ == "__main__":
    unittest.main(verbosity=2)
