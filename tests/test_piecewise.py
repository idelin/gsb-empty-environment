import unittest
from fractions import Fraction

from ratpiece import Interval, PiecewiseModel, Poly, RationalFunc


def rf(num, den=(1,)):
    return RationalFunc(Poly(num), Poly(den))


def build_f():
    f = PiecewiseModel("f")
    f.set_piece(Interval.open(None, 1), RationalFunc.identity(), "f1=x")
    f.set_piece(Interval.closed(1, 3), RationalFunc.constant(2), "f2=2")
    return f


def build_g():
    g = PiecewiseModel("g")
    # (x^2-1)/(x-1) = x+1，在 x=1 有可去空洞
    g.set_piece(Interval.open(0, 5), rf((-1, 0, 1), (-1, 1)), "g1")
    return g


class TestPiecewise(unittest.TestCase):
    def test_automatic_subdivision(self):
        f = build_f()
        self.assertEqual(f.boundaries, [Fraction(1), Fraction(3)])
        self.assertEqual(len(f.segs), 3)
        self.assertEqual(len(f.points), 2)

    def test_evaluate(self):
        f = build_f()
        self.assertEqual(f.evaluate(0), Fraction(0))
        self.assertEqual(f.evaluate(1), Fraction(2))
        self.assertEqual(f.evaluate(3), Fraction(2))
        self.assertIsNone(f.evaluate(4))

    def test_combine_add_values(self):
        h = PiecewiseModel.combine(build_f(), build_g(), "add", "h")
        self.assertEqual(h.evaluate(Fraction(1, 2)), Fraction(2))
        self.assertEqual(h.evaluate(2), Fraction(5))
        self.assertIsNone(h.evaluate(1))        # g 的空洞保留未定义
        self.assertIsNone(h.evaluate(4))        # f 在 3 之外无定义

    def test_combine_mul_values(self):
        m = PiecewiseModel.combine(build_f(), build_g(), "mul", "m")
        self.assertEqual(m.evaluate(2), Fraction(6))
        self.assertIsNone(m.evaluate(1))

    def test_no_merge_across_excluded_point(self):
        h = PiecewiseModel.combine(build_f(), build_g(), "add", "h")
        intervals = [p.interval.to_human() for p in h.pieces_out()]
        self.assertIn("(0, 1)", intervals)
        self.assertIn("(1, 3]", intervals)      # f2 在 3 处闭端，h(3) 有定义
        # 相同表达式在空洞两侧也不得合并：g + 0 后 (0,1) 与 (1,5) 同为 x+1
        z = PiecewiseModel("z")
        z.set_piece(Interval.open(0, 5), RationalFunc.constant(0), "zero")
        h2 = PiecewiseModel.combine(build_g(), z, "add", "h2")
        exprs = [p.expr.to_human() for p in h2.pieces_out()]
        self.assertEqual(exprs, ["x + 1", "x + 1"])
        pieces = [p.interval.to_human() for p in h2.pieces_out()]
        self.assertEqual(pieces, ["(0, 1)", "(1, 5)"])   # 跨空洞不合并

    def test_open_closed_preserved_after_combine(self):
        f = PiecewiseModel("f")
        f.set_piece(Interval.closed(0, 2), RationalFunc.constant(1), "a")
        g = PiecewiseModel("g")
        g.set_piece(Interval.closed(0, 2), RationalFunc.constant(1), "b")
        h = PiecewiseModel.combine(f, g, "add", "h")
        self.assertEqual(h.evaluate(0), Fraction(2))
        self.assertEqual(h.evaluate(2), Fraction(2))
        self.assertIsNone(h.evaluate(3))

    def test_incremental_edit_updates_cells(self):
        f = build_f()
        seg_before = [s.cid for s in f.segs]
        # 编辑：把 f2 改为 [1, 4] 上的常数 5
        pid = 2
        f.replace_piece(pid, Interval.closed(1, 4), RationalFunc.constant(5), "f2'=5")
        self.assertEqual(f.boundaries, [Fraction(1), Fraction(4)])
        self.assertEqual(f.evaluate(2), Fraction(5))
        self.assertEqual(f.evaluate(4), Fraction(5))    # 闭端点保留
        self.assertIsNone(f.evaluate(5))
        # 未受影响的左侧单元保持原样（增量维护）
        leftmost = f.segs[0]
        self.assertEqual(leftmost.cid, seg_before[0])

    def test_remove_piece_garbage_collects_boundary(self):
        f = build_f()
        f.remove_piece(2)                       # 删掉 [1,3] 上的常数段
        self.assertEqual(f.boundaries, [Fraction(1)])
        self.assertIsNone(f.evaluate(2))
        self.assertEqual(f.evaluate(0), Fraction(0))

    def test_source_mapping(self):
        f = build_f()
        _, point, _ = f.cells_around(Fraction(1))
        self.assertEqual(f.cell_sources(point), ["f2=2"])
        seg = f.segs[0]
        self.assertEqual(f.cell_sources(seg), ["f1=x"])

    def test_overlapping_pieces_sum(self):
        f = PiecewiseModel("f")
        f.set_piece(Interval.closed(0, 2), RationalFunc.constant(1), "a")
        f.set_piece(Interval.closed(1, 3), RationalFunc.constant(10), "b")
        self.assertEqual(f.evaluate(Fraction(3, 2)), Fraction(11))
        self.assertEqual(f.evaluate(Fraction(1, 2)), Fraction(1))

    def test_hole_boundary_created(self):
        g = build_g()
        self.assertIn(Fraction(1), g.boundaries)
        self.assertIsNone(g.evaluate(1))
        self.assertEqual(g.evaluate(2), Fraction(3))

    def test_combine_point_inside_other_models_segment(self):
        # 0 是 g 的边界，但落在 f 的段内部：h(0) 仍应精确定义
        f = build_f()
        g = PiecewiseModel("g")
        g.set_piece(Interval.closed_open(0, 5), rf((1, 1)), "g1: x+1")
        h = PiecewiseModel.combine(f, g, "add", "h")
        self.assertEqual(h.evaluate(0), Fraction(1))
        self.assertEqual(h.evaluate(3), Fraction(6))   # 两侧闭端点保持

    def test_point_cell_expr_undefined_hole_skipped(self):
        # 并集边界处一侧有可去空洞：该点不生成片段
        h = PiecewiseModel.combine(build_f(), build_g(), "add", "h")
        self.assertIsNone(h.evaluate(1))
        for piece in h.pieces_out():
            self.assertFalse(piece.interval.is_point()
                             and piece.interval.lo == 1)


if __name__ == "__main__":
    unittest.main()
