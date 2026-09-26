import unittest

from rational_pieces import (
    CombinedPiecewise, Interval, Piece, Piecewise, Poly, RationalFunction,
)


def const(c):
    return RationalFunction.of(Poly([c]), label=str(c))


def build():
    f = Piecewise([
        Piece('a', Interval(0, 1, True, True), const(1)),
        Piece('b', Interval(1, 2, False, True), const(2)),
        Piece('c', Interval(2, 3, False, True), const(3)),
    ])
    g = Piecewise.single(Interval(0, 3, True, True),
                         RationalFunction.of(Poly.x(), label='x'), pid='g0')
    return CombinedPiecewise(f, g, 'add')


class TestIncremental(unittest.TestCase):
    def test_edit_recomputes_only_affected(self):
        h = build()
        before = {id(s): s for s in h.segments}
        h.edit('L', 'a', func=const(10))        # 只影响含 L:a 的段
        after = h.segments
        reused = [s for s in after if id(s) in before]
        self.assertEqual(len(reused), 2)        # 另两段对象原样复用
        affected = [s for s in after if id(s) not in before]
        self.assertEqual(len(affected), 1)
        self.assertEqual(affected[0].sources, ('L:a', 'R:g0'))
        self.assertEqual(affected[0].func.value_at(0), 10)  # 10 + 0

    def test_edit_interval_updates_refinement(self):
        h = build()
        h.edit('L', 'a', interval=Interval(0, 1, True, False))  # 改开闭
        seg = [s for s in h.segments if 'L:a' in s.sources][0]
        self.assertEqual(str(seg.interval), '[0, 1)')
        # 接点 1 两侧均为开端点：左极限 1+1=2，右极限 2+1=3 -> 跳变
        b1 = [b for b in h.analyze().boundaries if b.point == 1][0]
        self.assertEqual(b1.kind, 'jump')

    def test_edit_tracks_source_mapping(self):
        h = build()
        h.edit('R', 'g0', func=const(0))        # 右侧整体换表达式
        for s in h.segments:
            self.assertIn('R:g0', s.sources)    # 来源映射保持
        vals = sorted(s.func.value_at(s.interval.lo) for s in h.segments)
        self.assertEqual(vals, [1, 2, 3])       # 现在每段就是左片常数

    def test_edit_rejects_overlap(self):
        h = build()
        with self.assertRaises(ValueError):
            h.edit('L', 'a', interval=Interval(0, 2, True, True))  # 与 b 重叠
        # 状态未被破坏
        self.assertEqual(len(h.segments), 3)

    def test_edit_continuity_follows(self):
        h = build()
        # 把片段 b 改成使接点 1 连续：左极限 1+1=2，右侧 2+1=3 -> 跳变
        rep = h.analyze()
        b1 = [b for b in rep.boundaries if b.point == 1][0]
        self.assertEqual(b1.kind, 'jump')
        h.edit('L', 'b', func=const(1))         # 改成 1 后两侧均为 x+1
        # 同表达式且接点被覆盖 -> 自动合并为一段，x=1 不再是边界
        self.assertEqual(len(h.segments), 2)
        self.assertEqual(str(h.segments[0].interval), '[0, 2]')
        rep2 = h.analyze()
        self.assertEqual([b for b in rep2.boundaries if b.point == 1], [])


if __name__ == '__main__':
    unittest.main()
