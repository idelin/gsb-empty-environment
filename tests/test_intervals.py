"""有限区间模型上的组合性质测试。"""
import random
import unittest

from posmap.intervals import IntervalMap, Span


def rand_span(rng, doc, hi=8):
    a = rng.randint(0, hi - 1)
    b = rng.randint(a + 1, hi)
    return Span(doc, a, b)


def rand_map(rng, src, dst):
    m = IntervalMap(src, dst)
    for i in range(rng.randint(0, 5)):
        m.add(rand_span(rng, src), rand_span(rng, dst), (f"{src}{i}",))
    return m


def relation(m):
    return {(b.src, b.dst, b.tag) for b in m.blocks}


class TestSpan(unittest.TestCase):
    def test_overlap_intersection(self):
        a = Span("d", 0, 5)
        b = Span("d", 3, 9)
        c = Span("d", 5, 7)
        self.assertTrue(a.overlaps(b))
        self.assertEqual(a.intersection(b), Span("d", 3, 5))
        self.assertFalse(a.overlaps(c))
        self.assertIsNone(a.intersection(c))
        self.assertFalse(a.overlaps(Span("x", 0, 5)))

    def test_contains_shift(self):
        self.assertTrue(Span("d", 0, 10).contains(Span("d", 2, 4)))
        self.assertEqual(Span("d", 1, 3).shift(10), Span("d", 11, 13))


class TestCompose(unittest.TestCase):
    def test_associativity_finite_interval_model(self):
        """有限区间模型：随机小映射上验证 (M1∘M2)∘M3 == M1∘(M2∘M3)。"""
        rng = random.Random(0xC0FFEE)
        for _ in range(500):
            m1 = rand_map(rng, "A", "B")
            m2 = rand_map(rng, "B", "C")
            m3 = rand_map(rng, "C", "D")
            left = m1.compose(m2).compose(m3)
            right = m1.compose(m2.compose(m3))
            self.assertEqual(relation(left), relation(right))

    def test_composed_lookup_matches_layered(self):
        """组合映射查询与逐层查询一致（两个方向）。"""
        rng = random.Random(42)
        for _ in range(200):
            m1 = rand_map(rng, "A", "B")
            m2 = rand_map(rng, "B", "C")
            comp = m1.compose(m2)
            for p in range(8):
                q = Span("A", p, p + 1)
                layered = set()
                for b1 in m1.targets(q):
                    for b2 in m2.targets(b1.dst):
                        layered.add(b2.dst)
                composed = {b.dst for b in comp.targets(q)}
                self.assertEqual(layered, composed)
                r = Span("C", p, p + 1)
                layered_src = set()
                for b2 in m2.sources(r):
                    for b1 in m1.sources(b2.src):
                        layered_src.add(b1.src)
                composed_src = {b.src for b in comp.sources(r)}
                self.assertEqual(layered_src, composed_src)

    def test_one_to_many_preserved(self):
        m1 = IntervalMap("A", "B")
        m1.add(Span("A", 0, 2), Span("B", 0, 1))
        m1.add(Span("A", 0, 2), Span("B", 5, 6))
        m2 = IntervalMap("B", "C")
        m2.add(Span("B", 0, 3), Span("C", 0, 1))
        m2.add(Span("B", 4, 6), Span("C", 9, 10))
        comp = m1.compose(m2)
        dsts = sorted(
            (b.dst for b in comp.targets(Span("A", 0, 1))),
            key=lambda s: (s.start, s.end),
        )
        self.assertEqual(dsts, [Span("C", 0, 1), Span("C", 9, 10)])

    def test_layer_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            IntervalMap("A", "B").compose(IntervalMap("X", "Y"))


if __name__ == "__main__":
    unittest.main()
