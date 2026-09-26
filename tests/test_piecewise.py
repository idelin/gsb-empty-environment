import unittest
from fractions import Fraction

from rational_pieces import (
    CombinedPiecewise, Interval, IsolationBudget, Piece, Piecewise, Poly,
    RationalFunction,
)


def const(c):
    return RationalFunction.of(Poly([c]), label=str(c))


def make_f():
    # f: [0,1] -> 1, (1,2] -> x
    return Piecewise([
        Piece('a', Interval(0, 1, True, True), const(1)),
        Piece('b', Interval(1, 2, False, True),
              RationalFunction.of(Poly.x(), label='x')),
    ])


class TestCombine(unittest.TestCase):
    def test_refinement_and_source_mapping(self):
        f = make_f()
        g = Piecewise.single(Interval(0, 2, True, True),
                             RationalFunction.of(Poly.x(), label='g'), pid='g0')
        h = CombinedPiecewise(f, g, 'add')
        segs = h.segments
        self.assertEqual(len(segs), 2)
        # 区间开闭在组合后保持
        self.assertEqual(str(segs[0].interval), '[0, 1]')
        self.assertEqual(str(segs[1].interval), '(1, 2]')
        # 来源映射
        self.assertEqual(segs[0].sources, ('L:a', 'R:g0'))
        self.assertEqual(segs[1].sources, ('L:b', 'R:g0'))
        # 表达式精确：x+1 与 2x
        self.assertEqual(segs[0].func.value_at(1), 2)
        self.assertEqual(segs[1].func.value_at(2), 4)

    def test_holes_stay_undefined(self):
        f = Piecewise.single(Interval(0, 2, True, True), const(1), pid='f0')
        g = Piecewise.single(Interval(1, 3, True, True), const(2), pid='g0')
        h = CombinedPiecewise(f, g, 'add')
        segs = h.segments
        self.assertEqual(len(segs), 1)              # 仅交叠区 [1,2]
        self.assertEqual(str(segs[0].interval), '[1, 2]')
        # 完全无交叠 -> 无细分段
        g2 = Piecewise.single(Interval(3, 4, True, True), const(2), pid='g1')
        self.assertEqual(CombinedPiecewise(f, g2, 'add').segments, [])

    def test_multiplication(self):
        f = Piecewise.single(Interval(0, 2, True, True),
                             RationalFunction.of(Poly.x(), label='x'), pid='f0')
        g = Piecewise.single(Interval(1, 3, True, True),
                             RationalFunction.of(Poly([1, 1]), label='x+1'), pid='g0')
        h = CombinedPiecewise(f, g, 'mul')
        seg = h.segments[0]
        self.assertEqual(str(seg.interval), '[1, 2]')
        self.assertEqual(seg.func.value_at(2), 6)   # 2 * 3

    def test_no_merge_across_excluded_point(self):
        # 右侧两片表达式相同，但接点 1 被两侧同时排除 -> 不合并
        f = Piecewise.single(Interval(0, 2, True, True), const(1), pid='f0')
        g = Piecewise([
            Piece('g1', Interval(0, 1, True, False), const(3)),
            Piece('g2', Interval(1, 2, False, True), const(3)),
        ])
        h = CombinedPiecewise(f, g, 'add')
        self.assertEqual(len(h.segments), 2)        # 跨排除点不合并

    def test_merge_when_junction_covered(self):
        # 接点 1 被左片闭端点覆盖且表达式相同 -> 允许合并
        f = Piecewise.single(Interval(0, 2, True, True), const(1), pid='f0')
        g = Piecewise([
            Piece('g1', Interval(0, 1, True, True), const(3)),
            Piece('g2', Interval(1, 2, False, True), const(3)),
        ])
        h = CombinedPiecewise(f, g, 'add')
        self.assertEqual(len(h.segments), 1)
        self.assertEqual(str(h.segments[0].interval), '[0, 2]')
        self.assertEqual(set(h.segments[0].sources), {'L:f0', 'R:g1', 'R:g2'})


class TestContinuity(unittest.TestCase):
    def test_jump_reported_exactly(self):
        f = Piecewise([
            Piece('a', Interval(0, 1, True, True), const(1)),
            Piece('b', Interval(1, 2, False, True), const(2)),
        ])
        rep = f.analyze()
        b1 = [b for b in rep.boundaries if b.point == 1][0]
        self.assertEqual(b1.kind, 'jump')
        self.assertEqual(b1.left.value, 1)
        self.assertEqual(b1.right.value, 2)
        self.assertEqual(rep.verdict, 'discontinuous')

    def test_continuous_on_whole_line(self):
        x = RationalFunction.of(Poly.x(), label='x')
        f = Piecewise([
            Piece('a', Interval(None, 0, False, True), x),
            Piece('b', Interval(0, None, False, False), x),
        ])
        rep = f.analyze()
        self.assertEqual(rep.verdict, 'continuous')
        self.assertTrue(rep.is_continuous_everywhere)

    def test_point_hole_not_continuous(self):
        x = RationalFunction.of(Poly.x(), label='x')
        f = Piecewise([
            Piece('a', Interval(0, 1, True, False), x),
            Piece('b', Interval(1, 2, False, True), x),
        ])
        rep = f.analyze()
        b1 = [b for b in rep.boundaries if b.point == 1][0]
        self.assertEqual(b1.kind, 'hole')           # 极限存在但排除点无定义
        self.assertEqual(rep.verdict, 'discontinuous')

    def test_infinite_discontinuity(self):
        g = RationalFunction.of(Poly([1]), Poly([-1, 1]), 'g')  # 1/(x-1)
        f = Piecewise([
            Piece('a', Interval(0, 1, True, False), g),
            Piece('b', Interval(1, 2, False, True), g),
        ])
        rep = f.analyze()
        b1 = [b for b in rep.boundaries if b.point == 1][0]
        self.assertEqual(b1.kind, 'infinite')
        self.assertEqual(b1.left.sign, -1)
        self.assertEqual(b1.right.sign, 1)

    def test_one_sided_not_claimed_continuous(self):
        f = Piecewise.single(Interval(0, 1, True, True), const(5), pid='a')
        rep = f.analyze()
        kinds = {b.kind for b in rep.boundaries}
        self.assertEqual(kinds, {'one_sided'})
        self.assertEqual(rep.verdict, 'continuous_on_domain')
        self.assertFalse(rep.is_continuous_everywhere)

    def test_interior_pole_exact_location(self):
        g = RationalFunction.of(Poly([1]), Poly([-2, 1]), 'g')  # 1/(x-2)
        f = Piecewise.single(Interval(0, 4, True, True), g, pid='a')
        rep = f.analyze()
        poles = [r for r in rep.interiors if r.kind == 'pole']
        self.assertEqual(len(poles), 1)
        self.assertIn('x = 2', poles[0].location)   # 有理根精确命名
        self.assertEqual(rep.verdict, 'discontinuous')

    def test_interior_removable_singularity(self):
        g = RationalFunction.of(Poly([-1, 0, 1]), Poly([-1, 1]), 'g')  # (x^2-1)/(x-1)
        f = Piecewise.single(Interval(0, 3, True, True), g, pid='a')
        rep = f.analyze()
        rem = [r for r in rep.interiors if r.kind == 'removable']
        self.assertEqual(len(rem), 1)
        self.assertIn('x = 1', rem[0].location)

    def test_irrational_pole_isolated_interval(self):
        g = RationalFunction.of(Poly([1]), Poly([-2, 0, 1]), 'g')  # 1/(x^2-2)
        f = Piecewise.single(Interval(0, 2, True, True), g, pid='a')
        rep = f.analyze()
        poles = [r for r in rep.interiors if r.kind == 'pole']
        self.assertEqual(len(poles), 1)
        self.assertIn('隔离区间', poles[0].location)  # 无理根用隔离区间表示

    def test_budget_exhaustion_verdict_undecided(self):
        den = Poly([-2, 0, 1]) * Poly([-8, 0, 1])   # 根 ±√2, ±2√2
        g = RationalFunction.of(Poly([1]), den, 'g')
        f = Piecewise.single(Interval(0, 3, True, True), g, pid='a')
        rep = f.analyze(IsolationBudget(max_steps=0))
        self.assertEqual(rep.verdict, 'undecided')
        self.assertTrue(any(r.kind == 'undecided' for r in rep.interiors))

    def test_gap_between_pieces(self):
        f = Piecewise([
            Piece('a', Interval(0, 1, True, True), const(1)),
            Piece('b', Interval(2, 3, True, True), const(2)),
        ])
        rep = f.analyze()
        ones = [b for b in rep.boundaries if b.kind == 'one_sided']
        self.assertEqual(len(ones), 4)  # 两个外端点 + 间隙两侧
        self.assertEqual(rep.verdict, 'continuous_on_domain')


if __name__ == '__main__':
    unittest.main()
