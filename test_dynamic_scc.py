import random
import unittest
from collections import Counter

from dynamic_scc import DynamicSCC, reference_index, apply_diff


class ScenarioTests(unittest.TestCase):
    def test_merge_then_split(self):
        idx = DynamicSCC()
        idx.apply_batch([("add_edge", "A", "B"), ("add_edge", "B", "C")])
        self.assertEqual(idx.components(),
                         {"A": frozenset("A"), "B": frozenset("B"),
                          "C": frozenset("C")})
        self.assertEqual(idx.condensation_edges(),
                         {("A", "B"): 1, ("B", "C"): 1})

        d = idx.apply_batch([("add_edge", "C", "A")])
        self.assertEqual(idx.components(), {"A": frozenset({"A", "B", "C"})})
        self.assertEqual(idx.condensation_edges(), {})
        self.assertEqual(d.merges, [("A", ("A", "B", "C"))])
        self.assertEqual(d.removed_components,
                         {"A": frozenset("A"), "B": frozenset("B"),
                          "C": frozenset("C")})
        self.assertEqual(d.added_components,
                         {"A": frozenset({"A", "B", "C"})})
        self.assertEqual(d.cond_edges_removed,
                         {("A", "B"): 1, ("B", "C"): 1})

        d = idx.apply_batch([("remove_edge", "B", "C")])
        self.assertEqual(idx.components(),
                         {"A": frozenset("A"), "B": frozenset("B"),
                          "C": frozenset("C")})
        self.assertEqual(idx.condensation_edges(),
                         {("A", "B"): 1, ("C", "A"): 1})
        self.assertEqual(d.splits, [("A", ("A", "B", "C"))])

    def test_unrelated_component_identity_stable(self):
        idx = DynamicSCC()
        idx.apply_batch([("add_edge", "X", "Y"), ("add_edge", "Y", "X"),
                         ("add_edge", "A", "B"), ("add_edge", "B", "C")])
        self.assertEqual(idx.component_of("X"), "X")
        before = idx.components()["X"]

        for ops in ([("add_edge", "C", "A")],
                    [("remove_edge", "B", "C")],
                    [("add_edge", "A", "C"), ("add_edge", "C", "B")]):
            d = idx.apply_batch(ops)
            self.assertNotIn("X", d.added_components)
            self.assertNotIn("X", d.removed_components)
            for key in (*d.cond_edges_added, *d.cond_edges_removed,
                        *d.cond_edges_count_changed):
                self.assertNotIn("X", key)
            self.assertEqual(idx.components()["X"], before)
            self.assertEqual(idx.component_of("Y"), "X")

    def test_parallel_edges(self):
        idx = DynamicSCC()
        idx.apply_batch([("add_edge", "A", "B"), ("add_edge", "A", "B"),
                         ("add_edge", "B", "A")])
        self.assertEqual(idx.components(), {"A": frozenset({"A", "B"})})

        # Removing one of two parallel edges must NOT disconnect the pair.
        d = idx.apply_batch([("remove_edge", "A", "B")])
        self.assertEqual(idx.components(), {"A": frozenset({"A", "B"})})
        self.assertEqual(d.added_components, {})
        self.assertEqual(d.removed_components, {})

        d = idx.apply_batch([("remove_edge", "A", "B")])
        self.assertEqual(idx.components(),
                         {"A": frozenset("A"), "B": frozenset("B")})
        self.assertEqual(idx.condensation_edges(), {("B", "A"): 1})
        self.assertEqual(d.splits, [("A", ("A", "B"))])

    def test_node_deletion_clears_edges_and_evidence(self):
        idx = DynamicSCC()
        idx.apply_batch([("add_edge", "A", "B"), ("add_edge", "B", "C"),
                         ("add_edge", "C", "A")])
        self.assertEqual(idx.components(), {"A": frozenset({"A", "B", "C"})})

        d = idx.apply_batch([("remove_node", "B")])
        self.assertEqual(idx.components(),
                         {"A": frozenset("A"), "C": frozenset("C")})
        self.assertEqual(idx.condensation_edges(), {("C", "A"): 1})
        self.assertIsNone(idx.component_of("B"))
        self.assertEqual(d.removed_components,
                         {"A": frozenset({"A", "B", "C"})})
        self.assertEqual(d.splits, [("A", ("A", "C"))])
        # The deleted node's component evidence is gone from every version
        # at or after the deletion.
        self.assertNotIn("B", idx.components().get("A", ()))

    def test_isolated_nodes(self):
        idx = DynamicSCC()
        d = idx.apply_batch([("add_node", "iso")])
        self.assertEqual(idx.components(), {"iso": frozenset({"iso"})})
        self.assertEqual(idx.condensation_edges(), {})
        self.assertEqual(d.formed, {"iso": frozenset({"iso"})})

        d = idx.apply_batch([("remove_node", "iso")])
        self.assertEqual(idx.components(), {})
        self.assertEqual(d.dissolved, {"iso": frozenset({"iso"})})

    def test_invalid_batch_rolls_back(self):
        idx = DynamicSCC()
        idx.apply_batch([("add_edge", "A", "B")])
        version = idx.version
        state = (idx.components(), idx.condensation_edges())

        bad_batches = [
            [("add_edge", "C", "D"), ("remove_edge", "X", "Y")],
            [("add_edge", "C", "D"), ("remove_node", "ZZZ")],
            [("add_node", "A")],
            [("remove_edge", "A", "B"), ("remove_edge", "A", "B")],
            [("frobnicate", "A")],
        ]
        for batch in bad_batches:
            with self.assertRaises(ValueError):
                idx.apply_batch(batch)
            self.assertEqual(idx.version, version)
            self.assertEqual((idx.components(), idx.condensation_edges()),
                             state)
        self.assertNotIn("C", idx.components())
        self.assertNotIn("D", idx.components())

    def test_versioned_queries(self):
        idx = DynamicSCC()
        idx.apply_batch([("add_edge", "A", "B")])          # v1
        idx.apply_batch([("add_edge", "B", "A")])          # v2
        idx.apply_batch([("add_node", "solo")])            # v3

        self.assertEqual(idx.components(version=1),
                         {"A": frozenset("A"), "B": frozenset("B")})
        self.assertEqual(idx.condensation_edges(version=1),
                         {("A", "B"): 1})
        self.assertEqual(idx.components(version=2),
                         {"A": frozenset({"A", "B"})})
        self.assertEqual(idx.component_of("B", version=1), "B")
        self.assertEqual(idx.component_of("B", version=2), "A")
        self.assertEqual(idx.component_of("solo", version=2), None)
        self.assertEqual(idx.component_of("solo", version=3), "solo")
        self.assertEqual(idx.components(version=0), {})
        with self.assertRaises(KeyError):
            idx.components(version=99)


def _model_apply(nodes, edges, op):
    kind = op[0]
    if kind == "add_node":
        nodes.add(op[1])
    elif kind == "remove_node":
        nodes.discard(op[1])
        for key in [k for k in edges if op[1] in k]:
            del edges[key]
    elif kind == "add_edge":
        nodes.update((op[1], op[2]))
        edges[(op[1], op[2])] += 1
    else:
        edges[(op[1], op[2])] -= 1
        if edges[(op[1], op[2])] == 0:
            del edges[(op[1], op[2])]


class RandomizedDifferentialTest(unittest.TestCase):
    def test_random_batches_against_full_recompute(self):
        rng = random.Random(20260922)
        idx = DynamicSCC()
        nodes = set()
        edges = Counter()
        replay_comps = {}
        replay_cond = {}
        names = [f"n{i}" for i in range(10)]

        for step in range(400):
            batch = []
            for i in range(rng.randint(1, 5)):
                roll = rng.random()
                if roll < 0.40:
                    op = ("add_edge", rng.choice(names), rng.choice(names))
                elif roll < 0.60 and edges:
                    op = ("remove_edge", *rng.choice(list(edges)))
                elif roll < 0.70:
                    op = ("add_node", f"x{step}_{i}")
                    if op[1] in nodes:
                        continue
                elif roll < 0.85 and nodes:
                    op = ("remove_node", rng.choice(sorted(nodes)))
                else:
                    op = ("add_edge", rng.choice(names), rng.choice(names))
                batch.append(op)
                _model_apply(nodes, edges, op)

            diff = idx.apply_batch(batch)
            ref_comps, ref_cond = reference_index(
                nodes, list(edges.elements()))

            # 1) membership and condensation match a full recompute
            self.assertEqual(idx.components(), ref_comps,
                             f"step {step}: {batch}")
            self.assertEqual(idx.condensation_edges(), ref_cond,
                             f"step {step}: {batch}")

            # 2) consuming the diff stream reproduces the same state
            apply_diff(replay_comps, replay_cond, diff)
            self.assertEqual(replay_comps, ref_comps,
                             f"replay diverged at step {step}")
            self.assertEqual(replay_cond, ref_cond,
                             f"replay diverged at step {step}")

            # 3) components partition exactly the live node set
            covered = set().union(*ref_comps.values()) if ref_comps else set()
            self.assertEqual(covered, nodes)

            # 4) pinned-version queries stay consistent with history
            if step % 50 == 0 and idx.version > 0:
                snap = idx.components(version=idx.version)
                self.assertEqual(snap, ref_comps)


if __name__ == "__main__":
    unittest.main()
