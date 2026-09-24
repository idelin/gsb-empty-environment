import unittest
from fractions import Fraction

from planar_graph import Segment, build_planar_graph


def edge_map(g):
    """{(端点坐标对): sources} 便于断言。"""
    return {
        (g.vertices[e.v1], g.vertices[e.v2]): e.sources
        for e in g.edges
    }


class TestPlanarGraph(unittest.TestCase):
    def test_cross_gives_four_edges_and_shared_vertex(self):
        g = build_planar_graph([
            Segment.of(0, 0, 4, 4, "a"),
            Segment.of(0, 4, 4, 0, "b"),
        ])
        center = (Fraction(2), Fraction(2))
        self.assertEqual(len(g.vertices), 5)          # 交点只建一次
        self.assertEqual(len(g.edges), 4)             # 十字 -> 4 条子边
        for e in g.edges:
            self.assertIn(center, (g.vertices[e.v1], g.vertices[e.v2]))
        self.assertEqual(len(edge_map(g)), 4)

    def test_identical_segments_keep_dual_sources(self):
        g = build_planar_graph([
            Segment.of(0, 0, 4, 0, "s1"),
            Segment.of(0, 0, 4, 0, "s2"),
        ])
        self.assertEqual(len(g.vertices), 2)
        self.assertEqual(len(g.edges), 1)
        self.assertEqual(g.edges[0].sources, ("s1", "s2"))

    def test_partial_collinear_overlap(self):
        g = build_planar_graph([
            Segment.of(0, 0, 6, 0, "a"),
            Segment.of(3, 0, 9, 0, "b"),
        ])
        em = edge_map(g)
        self.assertEqual(len(em), 3)
        z, t = Fraction(0), Fraction(3)
        self.assertEqual(em[((z, z), (t, z))], ("a",))
        self.assertEqual(em[((t, z), (Fraction(6), z))], ("a", "b"))  # 重叠段双来源
        self.assertEqual(em[((Fraction(6), z), (Fraction(9), z))], ("b",))

    def test_shared_endpoint_not_duplicated(self):
        g = build_planar_graph([
            Segment.of(0, 0, 1, 0, "a"),
            Segment.of(1, 0, 1, 1, "b"),
            Segment.of(1, 0, 2, 0, "c"),
        ])
        self.assertEqual(len(g.vertices), 4)          # (1,0) 只出现一次
        self.assertEqual(len(g.edges), 3)
        shared = g.vertices.index((Fraction(1), Fraction(0)))
        deg = sum(1 for e in g.edges if shared in (e.v1, e.v2))
        self.assertEqual(deg, 3)

    def test_t_junction(self):
        g = build_planar_graph([
            Segment.of(0, 0, 4, 0, "base"),
            Segment.of(2, 0, 2, 3, "stem"),
        ])
        self.assertEqual(len(g.edges), 3)             # base 被切成两段 + stem
        self.assertEqual(len(g.vertices), 4)

    def test_rational_coordinates_exact(self):
        g = build_planar_graph([
            Segment.of(0, 0, 1, 1, "a"),
            Segment.of("1/2", 0, "1/2", 1, "b"),
        ])
        mid = (Fraction(1, 2), Fraction(1, 2))
        self.assertIn(mid, g.vertices)
        self.assertEqual(len(g.edges), 4)

    def test_zero_length_segments_isolated_no_division(self):
        g = build_planar_graph([
            Segment.of(3, 3, 3, 3, "p1"),
            Segment.of(0, 0, 4, 0, "seg"),
            Segment.of(2, 0, 2, 0, "p2"),           # 零长度但落在 seg 上
        ])
        self.assertEqual(len(g.edges), 1)
        self.assertEqual(len(g.isolated_points), 2)
        by_label = {ip.label: ip for ip in g.isolated_points}
        self.assertEqual(by_label["p1"].on_segments, ())
        self.assertEqual(by_label["p2"].on_segments, ("seg",))

    def test_parallel_disjoint_and_touching_endpoint(self):
        g = build_planar_graph([
            Segment.of(0, 0, 2, 0, "a"),
            Segment.of(0, 1, 2, 1, "b"),            # 平行不相交
            Segment.of(2, 0, 3, 1, "c"),            # 端点接触 a
        ])
        self.assertEqual(len(g.edges), 3)
        self.assertEqual(len(g.vertices), 5)        # (2,0) 共享

    def test_collinear_touch_at_single_point(self):
        g = build_planar_graph([
            Segment.of(0, 0, 2, 0, "a"),
            Segment.of(2, 0, 4, 0, "b"),
        ])
        self.assertEqual(len(g.vertices), 3)
        self.assertEqual(len(g.edges), 2)

    def test_empty_and_single(self):
        self.assertEqual(build_planar_graph([]).edges, [])
        g = build_planar_graph([Segment.of(0, 0, 1, 1, "x")])
        self.assertEqual(len(g.vertices), 2)
        self.assertEqual(len(g.edges), 1)


if __name__ == "__main__":
    unittest.main()
