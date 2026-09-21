import unittest

from buildplanner.graph import condensation_stages, shortest_path, tarjan_scc


def scc_sets(components):
    return {frozenset(c) for c in components}


class TestTarjan(unittest.TestCase):
    def test_cycle_merges_into_one_component(self):
        edges = {"a": ["b"], "b": ["c"], "c": ["a"], "d": ["a"]}
        comps = tarjan_scc(list(edges), edges)
        self.assertEqual(
            scc_sets(comps), {frozenset({"a", "b", "c"}), frozenset({"d"})}
        )

    def test_disconnected_nodes_are_singletons(self):
        edges = {"x": [], "y": []}
        self.assertEqual(
            scc_sets(tarjan_scc(list(edges), edges)),
            {frozenset({"x"}), frozenset({"y"})},
        )

    def test_self_loop_is_cyclic_component(self):
        edges = {"s": ["s"]}
        self.assertEqual(scc_sets(tarjan_scc(list(edges), edges)), {frozenset({"s"})})


class TestStages(unittest.TestCase):
    def test_diamond_levels(self):
        edges = {"app": ["lib", "ui"], "lib": ["base"], "ui": ["base"], "base": []}
        comps = tarjan_scc(list(edges), edges)
        stages = condensation_stages(comps, edges)
        stage_of = {c[0]: s for c, s in zip(comps, stages)}
        self.assertEqual(stage_of["base"], 0)
        self.assertEqual(stage_of["lib"], 1)
        self.assertEqual(stage_of["ui"], 1)
        self.assertEqual(stage_of["app"], 2)

    def test_cycle_group_gets_single_stage(self):
        edges = {"a": ["b"], "b": ["a", "z"], "z": []}
        comps = tarjan_scc(list(edges), edges)
        stages = condensation_stages(comps, edges)
        by_comp = {frozenset(c): s for c, s in zip(comps, stages)}
        self.assertEqual(by_comp[frozenset({"z"})], 0)
        self.assertEqual(by_comp[frozenset({"a", "b"})], 1)


class TestPath(unittest.TestCase):
    def test_shortest_path(self):
        edges = {"a": ["b", "c"], "b": ["d"], "c": [], "d": []}
        self.assertEqual(shortest_path(edges, "a", "d"), ["a", "b", "d"])

    def test_no_path(self):
        self.assertIsNone(shortest_path({"a": [], "b": []}, "a", "b"))


if __name__ == "__main__":
    unittest.main()
