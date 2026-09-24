import unittest
from fractions import Fraction

from planar_segments import (
    build_planar_graph,
    point_on_segment,
    segment_intersection,
    to_fraction,
)

F = Fraction


def edge_sources(graph):
    return sorted(tuple(sorted(e.sources)) for e in graph.edges)


class TestSegmentIntersection(unittest.TestCase):
    def test_cross_point(self):
        hit = segment_intersection(((F(0), F(0)), (F(4), F(4))),
                                   ((F(0), F(4)), (F(4), F(0))))
        self.assertEqual(hit, ("point", (F(2), F(2))))

    def test_rational_cross_point(self):
        hit = segment_intersection(((F(0), F(0)), (F(1), F(1))),
                                   ((F(0), F(1)), (F(1), F(0))))
        self.assertEqual(hit, ("point", (F(1, 2), F(1, 2))))

    def test_parallel_disjoint(self):
        self.assertIsNone(segment_intersection(
            ((F(0), F(0)), (F(1), F(0))), ((F(0), F(1)), (F(1), F(1)))))

    def test_collinear_disjoint(self):
        self.assertIsNone(segment_intersection(
            ((F(0), F(0)), (F(1), F(0))), ((F(2), F(0)), (F(3), F(0)))))

    def test_partial_overlap(self):
        hit = segment_intersection(((F(0), F(0)), (F(6), F(0))),
                                   ((F(3), F(0)), (F(9), F(0))))
        self.assertEqual(hit, ("overlap", (F(3), F(0)), (F(6), F(0))))

    def test_overlap_touching_at_single_point(self):
        hit = segment_intersection(((F(0), F(0)), (F(2), F(0))),
                                   ((F(2), F(0)), (F(5), F(0))))
        self.assertEqual(hit, ("point", (F(2), F(0))))

    def test_vertical_overlap(self):
        hit = segment_intersection(((F(1), F(0)), (F(1), F(4))),
                                   ((F(1), F(2)), (F(1), F(8))))
        self.assertEqual(hit, ("overlap", (F(1), F(2)), (F(1), F(4))))

    def test_endpoint_touch(self):
        hit = segment_intersection(((F(0), F(0)), (F(2), F(2))),
                                   ((F(2), F(2)), (F(4), F(0))))
        self.assertEqual(hit, ("point", (F(2), F(2))))


class TestPlanarGraph(unittest.TestCase):
    def test_cross_splits_into_four_edges(self):
        graph = build_planar_graph([
            [[0, 0], [4, 4]],
            [[0, 4], [4, 0]],
        ])
        self.assertEqual(len(graph.vertices), 5)
        self.assertEqual(len(graph.edges), 4)
        self.assertEqual(edge_sources(graph), [(0,), (0,), (1,), (1,)])
        center = (F(2), F(2))
        self.assertIn(center, graph.vertices)
        for e in graph.edges:
            self.assertIn(center, (graph.vertices[e.u], graph.vertices[e.v]))

    def test_shared_endpoint_not_duplicated(self):
        graph = build_planar_graph([
            [[0, 0], [2, 0]],
            [[2, 0], [2, 2]],
            [[2, 2], [0, 0]],
        ])
        self.assertEqual(len(graph.vertices), 3)
        self.assertEqual(len(graph.edges), 3)

    def test_full_coincidence_keeps_dual_sources(self):
        graph = build_planar_graph([
            [[0, 0], [4, 0]],
            [[0, 0], [4, 0]],
        ])
        self.assertEqual(len(graph.vertices), 2)
        self.assertEqual(len(graph.edges), 1)
        self.assertEqual(graph.edges[0].sources, (0, 1))

    def test_partial_overlap_middle_edge_has_dual_sources(self):
        graph = build_planar_graph([
            [[0, 0], [6, 0]],
            [[3, 0], [9, 0]],
        ])
        self.assertEqual(len(graph.vertices), 4)
        self.assertEqual(len(graph.edges), 3)
        self.assertEqual(edge_sources(graph), [(0,), (0, 1), (1,)])

    def test_nested_overlap(self):
        graph = build_planar_graph([
            [[0, 0], [10, 0]],
            [[2, 0], [8, 0]],
        ])
        self.assertEqual(len(graph.edges), 3)
        self.assertEqual(edge_sources(graph), [(0,), (0,), (0, 1)])

    def test_t_junction(self):
        graph = build_planar_graph([
            [[0, 0], [4, 0]],
            [[2, 0], [2, 3]],
        ])
        self.assertEqual(len(graph.vertices), 4)
        self.assertEqual(len(graph.edges), 3)
        self.assertEqual(edge_sources(graph), [(0,), (0,), (1,)])

    def test_rational_coordinates(self):
        graph = build_planar_graph([
            [[0, 0], [1, 1]],
            [[0, 1], [1, 0]],
        ])
        self.assertIn((F(1, 2), F(1, 2)), graph.vertices)
        self.assertEqual(len(graph.edges), 4)

    def test_rational_string_input(self):
        graph = build_planar_graph([
            [["0", "0"], ["5/2", "0"]],
            [["1/2", "0"], ["7/4", "0"]],
        ])
        self.assertEqual(len(graph.edges), 3)
        self.assertEqual(edge_sources(graph), [(0,), (0,), (0, 1)])
        self.assertIn((F(7, 4), F(0)), graph.vertices)

    def test_zero_length_classified_not_splitting(self):
        graph = build_planar_graph([
            [[0, 0], [4, 0]],
            [[4, 0], [4, 0]],
            [[2, 0], [2, 0]],
            [[9, 9], [9, 9]],
        ])
        self.assertEqual(len(graph.edges), 1)  # 零长度段不触发分割
        kinds = {z.source: z.kind for z in graph.zero_length}
        self.assertEqual(kinds, {1: "vertex", 2: "on_edge", 3: "isolated"})
        zv = next(z for z in graph.zero_length if z.kind == "vertex")
        self.assertEqual(graph.vertices[zv.vertex], (F(4), F(0)))

    def test_only_zero_length_input(self):
        graph = build_planar_graph([[[3, 3], [3, 3]]])
        self.assertEqual(graph.vertices, [])
        self.assertEqual(graph.edges, [])
        self.assertEqual(graph.zero_length[0].kind, "isolated")

    def test_empty_input(self):
        graph = build_planar_graph([])
        self.assertEqual((graph.vertices, graph.edges, graph.zero_length),
                         ([], [], []))

    def test_duplicate_zero_length_points(self):
        graph = build_planar_graph([
            [[1, 1], [1, 1]],
            [[1, 1], [1, 1]],
        ])
        self.assertTrue(all(z.kind == "isolated" for z in graph.zero_length))

    def test_no_epsilon_boundary_point_counts_exactly(self):
        # 交点恰为 1/3：浮点 epsilon 方案易错，有理数精确命中。
        graph = build_planar_graph([
            [[0, 0], [1, 0]],
            [["1/3", 0], ["1/3", 1]],
        ])
        self.assertIn((F(1, 3), F(0)), graph.vertices)
        self.assertEqual(len(graph.edges), 3)

    def test_to_fraction_rejects_float(self):
        with self.assertRaises(TypeError):
            to_fraction(0.5)

    def test_point_on_segment(self):
        a, b = (F(0), F(0)), (F(4), F(0))
        self.assertTrue(point_on_segment((F(2), F(0)), a, b))
        self.assertFalse(point_on_segment((F(5), F(0)), a, b))
        self.assertFalse(point_on_segment((F(2), F(1)), a, b))


if __name__ == "__main__":
    unittest.main(verbosity=2)
