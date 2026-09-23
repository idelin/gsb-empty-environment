"""unittest suite for the dynamic multigraph decomposition."""

from __future__ import annotations

import random
import unittest

from dynamic_graph import DynamicMultigraph, Edge
from reference import full_analysis, separated_sets


def block_set(blocks):
    return {frozenset(b) for b in blocks}


class ParallelEdgeTests(unittest.TestCase):
    def test_two_parallel_edges_are_not_bridges(self):
        g = DynamicMultigraph()
        g.add_edges([("e1", "A", "B"), ("e2", "A", "B")])
        self.assertEqual(g.bridges(), frozenset())
        self.assertIsNone(g.separated_by("e1"))
        self.assertIsNone(g.separated_by("e2"))
        # the two parallel edges form one biconnected component
        self.assertEqual(block_set(g.biconnected_components()), {frozenset({"e1", "e2"})})
        self.assertEqual(g.articulation_points(), frozenset())

    def test_removing_one_parallel_edge_makes_the_other_a_bridge(self):
        g = DynamicMultigraph()
        g.add_edges([("e1", "A", "B"), ("e2", "A", "B")])
        g.remove_edges(["e1"])
        self.assertEqual(g.bridges(), frozenset({"e2"}))
        sides = g.separated_by("e2")
        self.assertIsNotNone(sides)
        self.assertEqual({sides[0], sides[1]}, {frozenset({"A"}), frozenset({"B"})})
        # a bridge is a singleton biconnected component
        self.assertEqual(block_set(g.biconnected_components()), {frozenset({"e2"})})

    def test_triple_parallel_edges(self):
        g = DynamicMultigraph()
        g.add_edges([("e1", "A", "B"), ("e2", "A", "B"), ("e3", "A", "B")])
        self.assertEqual(g.bridges(), frozenset())
        g.remove_edges(["e1", "e2"])
        self.assertEqual(g.bridges(), frozenset({"e3"}))


class TriangleWithTailTests(unittest.TestCase):
    def setUp(self):
        self.g = DynamicMultigraph()
        self.g.add_edges(
            [("ab", "A", "B"), ("bc", "B", "C"), ("ca", "C", "A"), ("cd", "C", "D")]
        )

    def test_tail_edge_is_the_only_bridge(self):
        self.assertEqual(self.g.bridges(), frozenset({"cd"}))

    def test_tail_attachment_is_the_only_articulation_point(self):
        self.assertEqual(self.g.articulation_points(), frozenset({"C"}))

    def test_blocks(self):
        self.assertEqual(
            block_set(self.g.biconnected_components()),
            {frozenset({"ab", "bc", "ca"}), frozenset({"cd"})},
        )

    def test_separated_by_tail(self):
        sides = self.g.separated_by("cd")
        self.assertEqual(
            {sides[0], sides[1]},
            {frozenset({"A", "B", "C"}), frozenset({"D"})},
        )
        self.assertIsNone(self.g.separated_by("ab"))


class SelfLoopTests(unittest.TestCase):
    def test_self_loop_is_never_a_bridge_and_forms_own_block(self):
        g = DynamicMultigraph()
        g.add_edges([("loop", "A", "A"), ("ab", "A", "B"), ("ac", "A", "C")])
        self.assertEqual(g.bridges(), frozenset({"ab", "ac"}))
        self.assertEqual(g.articulation_points(), frozenset({"A"}))
        self.assertIn(frozenset({"loop"}), block_set(g.biconnected_components()))
        self.assertIsNone(g.separated_by("loop"))

    def test_self_loop_does_not_prevent_bridge(self):
        # a self-loop on A must not make the A-B edge a non-bridge
        g = DynamicMultigraph()
        g.add_edges([("loop", "A", "A"), ("ab", "A", "B")])
        self.assertEqual(g.bridges(), frozenset({"ab"}))


class IsolatedVertexTests(unittest.TestCase):
    def test_isolated_vertices_are_kept(self):
        g = DynamicMultigraph()
        g.add_vertex("Z")
        g.add_edges([("ab", "A", "B")])
        g.remove_edges(["ab"])
        self.assertEqual(g.vertices, {"A", "B", "Z"})
        self.assertEqual(g.bridges(), frozenset())
        comps = {frozenset(c) for c in g.connected_components()}
        self.assertEqual(comps, {frozenset({"A"}), frozenset({"B"}), frozenset({"Z"})})

    def test_isolated_vertex_is_not_an_articulation_point(self):
        g = DynamicMultigraph()
        g.add_vertex("Z")
        g.add_edges([("ab", "A", "B")])
        self.assertEqual(g.articulation_points(), frozenset())


class IntervalTests(unittest.TestCase):
    def test_interval_is_left_closed_right_open(self):
        g = DynamicMultigraph()
        g.add_edges([("e", "A", "B", 2, 5)])
        self.assertEqual(g.bridges(t=1), frozenset())  # before start
        self.assertEqual(g.bridges(t=2), frozenset({"e"}))  # start included
        self.assertEqual(g.bridges(t=4), frozenset({"e"}))
        self.assertEqual(g.bridges(t=5), frozenset())  # end excluded
        self.assertIsNone(g.separated_by("e", t=5))
        sides = g.separated_by("e", t=2)
        self.assertEqual({sides[0], sides[1]}, {frozenset({"A"}), frozenset({"B"})})

    def test_intervals_change_decomposition_over_time(self):
        g = DynamicMultigraph()
        g.add_edges(
            [
                ("e1", "A", "B", 0, 10),
                ("e2", "A", "B", 3, 6),  # parallel to e1 only inside [3, 6)
            ]
        )
        self.assertEqual(g.bridges(t=2), frozenset({"e1"}))
        self.assertEqual(g.bridges(t=3), frozenset())
        self.assertEqual(g.bridges(t=5), frozenset())
        self.assertEqual(g.bridges(t=6), frozenset({"e1"}))

    def test_invalid_interval_rejected(self):
        g = DynamicMultigraph()
        with self.assertRaises(ValueError):
            g.add_edges([("bad", "A", "B", 5, 5)])


class BatchUpdateTests(unittest.TestCase):
    def test_batch_add_and_remove(self):
        g = DynamicMultigraph()
        g.add_edges(
            [
                ("ab", "A", "B"),
                ("bc", "B", "C"),
                ("ca", "C", "A"),
                ("cd", "C", "D"),
                ("de", "D", "E"),
            ]
        )
        self.assertEqual(g.bridges(), frozenset({"cd", "de"}))
        self.assertEqual(g.articulation_points(), frozenset({"C", "D"}))
        # batch remove + the remaining graph is re-analysed as a whole
        g.remove_edges(["cd", "de"])
        self.assertEqual(g.bridges(), frozenset())
        self.assertEqual(g.articulation_points(), frozenset())
        self.assertEqual(g.vertices, {"A", "B", "C", "D", "E"})  # D, E isolated but kept

    def test_duplicate_edge_id_rejected(self):
        g = DynamicMultigraph()
        g.add_edges([("e1", "A", "B")])
        with self.assertRaises(ValueError):
            g.add_edges([("e1", "A", "C")])

    def test_unknown_edge_removal_rejected(self):
        g = DynamicMultigraph()
        with self.assertRaises(KeyError):
            g.remove_edges(["nope"])


class ConsistencyWithFullAlgorithmTests(unittest.TestCase):
    """Random batch updates must match the brute-force full algorithm."""

    def test_random_batches_match_full_analysis(self):
        rng = random.Random(20260923)
        vertex_pool = ["A", "B", "C", "D", "E", "F"]
        times = [None, 0, 1, 2, 3, 4, 5, 6, 7]
        g = DynamicMultigraph()
        g.add_vertex("F")  # keep an isolated vertex around
        edge_seq = 0

        for step in range(120):
            # random batch of additions
            adds = []
            for _ in range(rng.randint(0, 3)):
                u = rng.choice(vertex_pool)
                v = rng.choice(vertex_pool)  # may be a self-loop
                start = rng.randint(0, 5)
                end = rng.randint(start + 1, 8)
                adds.append((f"e{edge_seq}", u, v, start, end))
                edge_seq += 1
            if adds:
                g.add_edges(adds)
            # random batch of removals
            live = list(g.edges)
            rng.shuffle(live)
            doomed = live[: rng.randint(0, min(3, len(live)))]
            if doomed:
                g.remove_edges(doomed)

            vertices = g.vertices
            edges = list(g.edges.values())
            for t in times:
                want = full_analysis(vertices, edges, t)
                self.assertEqual(g.bridges(t), want.bridges, f"bridges t={t} step={step}")
                self.assertEqual(
                    g.articulation_points(t),
                    want.articulation_points,
                    f"articulation points t={t} step={step}",
                )
                self.assertEqual(
                    block_set(g.biconnected_components(t)),
                    set(want.blocks),
                    f"blocks t={t} step={step}",
                )
                self.assertEqual(
                    {frozenset(c) for c in g.connected_components(t)},
                    set(want.components),
                    f"components t={t} step={step}",
                )
                for e in g.active_edges(t):
                    got = g.separated_by(e.id, t)
                    exp = separated_sets(vertices, edges, e.id, t)
                    if exp is None:
                        self.assertIsNone(got, f"separated t={t} edge={e.id}")
                    else:
                        self.assertEqual(
                            {got[0], got[1]},
                            {exp[0], exp[1]},
                            f"separated t={t} edge={e.id}",
                        )

    def test_known_structures_match(self):
        # a mixed structure: triangle, parallel edges, tail, self-loop, isolated vertex
        g = DynamicMultigraph()
        g.add_vertex("Z")
        g.add_edges(
            [
                ("ab", "A", "B"),
                ("bc", "B", "C"),
                ("ca", "C", "A"),
                ("cd1", "C", "D"),
                ("cd2", "C", "D"),
                ("de", "D", "E"),
                ("ee", "E", "E"),
            ]
        )
        want = full_analysis(g.vertices, list(g.edges.values()))
        self.assertEqual(g.bridges(), want.bridges)
        self.assertEqual(g.articulation_points(), want.articulation_points)
        self.assertEqual(block_set(g.biconnected_components()), set(want.blocks))
        self.assertEqual(g.bridges(), frozenset({"de"}))
        self.assertEqual(g.articulation_points(), frozenset({"C", "D"}))


if __name__ == "__main__":
    unittest.main()
