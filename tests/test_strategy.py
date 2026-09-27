import copy
import json
import unittest

from simcheck.fixpoint import FAIL, greatest_simulation
from simcheck.lts import LTS
from simcheck.strategy import build_strategy, extract_trace, verify_witness, witness_dict


def failing_result(impl_path, spec_path, mode="weak", divergence_sensitive=False):
    impl = LTS.load(impl_path)
    spec = LTS.load(spec_path)
    result = greatest_simulation(
        impl, spec, mode=mode, divergence_sensitive=divergence_sensitive
    )
    assert result.status == FAIL, result
    graph = build_strategy(
        impl, spec, mode, divergence_sensitive, result.relation, result.rank, result.rounds
    )
    return impl, spec, result, graph


class TestStrategyGraphShape(unittest.TestCase):
    def setUp(self):
        self.impl, self.spec, self.result, self.graph = failing_result(
            "examples/impl_stutter.json", "examples/spec_choice.json", mode="branching"
        )

    def test_root_is_initial_pair(self):
        root = self.graph.pair_nodes[self.graph.root]
        self.assertEqual((root.impl_state, root.spec_state), ("i0", "s0"))

    def test_ranks_strictly_decrease_along_responses(self):
        for pair_node in self.graph.pair_nodes:
            for attack_id in pair_node.attacks:
                attack_node = self.graph.attack_nodes[attack_id]
                for _response, target in attack_node.responses:
                    target_node = self.graph.pair_nodes[target]
                    self.assertLess(target_node.rank, pair_node.rank)

    def test_every_pair_node_has_an_attack(self):
        for pair_node in self.graph.pair_nodes:
            self.assertTrue(pair_node.attacks)

    def test_leaves_are_no_response_or_divergence(self):
        for attack_node in self.graph.attack_nodes:
            if attack_node.attack.kind == "divergence":
                self.assertEqual(attack_node.responses, [])

    def test_trace_is_replayable_projection(self):
        trace = extract_trace(self.graph)
        self.assertTrue(trace["steps"])
        self.assertIn(trace["terminal_reason"], ("no-response (implementation deadlocks or has no matching move)", "divergence"))
        first = trace["steps"][0]
        self.assertEqual(first["pair"], ["i0", "s0"])
        # the spec attack of the first step must be a real spec move
        spec_moves = {(lab, dst) for lab, dst in self.spec.successors("s0")}
        attack = first["attack"]
        self.assertIn((attack["action"], attack["spec_next"]), spec_moves)


class TestDeadlockWitness(unittest.TestCase):
    def test_deadlock_attack_has_no_responses(self):
        impl, spec, result, graph = failing_result(
            "examples/impl_deadlock.json", "examples/spec_progress.json"
        )
        root = graph.pair_nodes[graph.root]
        attack = graph.attack_nodes[root.attacks[0]]
        self.assertEqual(attack.attack.kind, "visible")
        self.assertEqual(attack.responses, [])
        trace = extract_trace(graph)
        self.assertIn("no-response", trace["terminal_reason"])


class TestDivergenceWitness(unittest.TestCase):
    def test_divergence_leaf(self):
        impl, spec, result, graph = failing_result(
            "examples/impl_divergent.json",
            "examples/spec_simple.json",
            divergence_sensitive=True,
        )
        kinds = {
            graph.attack_nodes[a].attack.kind
            for node in graph.pair_nodes
            for a in node.attacks
        }
        self.assertIn("divergence", kinds)
        trace = extract_trace(graph)
        self.assertEqual(trace["terminal_reason"], "divergence")


class TestReplay(unittest.TestCase):
    def _witness(self, impl_path, spec_path, **kwargs):
        impl, spec, result, graph = failing_result(impl_path, spec_path, **kwargs)
        return impl, spec, witness_dict(graph)

    def test_valid_witness_replays(self):
        impl, spec, witness = self._witness(
            "examples/impl_stutter.json", "examples/spec_choice.json", mode="branching"
        )
        self.assertEqual(verify_witness(impl, spec, witness), [])

    def test_valid_witness_replays_divergence(self):
        impl, spec, witness = self._witness(
            "examples/impl_divergent.json",
            "examples/spec_simple.json",
            divergence_sensitive=True,
        )
        self.assertEqual(verify_witness(impl, spec, witness), [])

    def test_tampered_rank_rejected(self):
        impl, spec, witness = self._witness(
            "examples/impl_stutter.json", "examples/spec_choice.json", mode="branching"
        )
        bad = copy.deepcopy(witness)
        bad["ranks"][0][2] += 1
        self.assertTrue(verify_witness(impl, spec, bad))

    def test_tampered_response_rejected(self):
        impl, spec, witness = self._witness(
            "examples/impl_stutter.json", "examples/spec_choice.json", mode="branching"
        )
        bad = copy.deepcopy(witness)
        for node in bad["strategy"]["nodes"]:
            if node["type"] == "attack" and node["responses"]:
                node["responses"] = node["responses"][1:] or node["responses"]
                node["responses"][0]["endpoint"] = "nowhere"
                break
        self.assertTrue(verify_witness(impl, spec, bad))

    def test_wrong_mode_rejected(self):
        impl, spec, witness = self._witness(
            "examples/impl_stutter.json", "examples/spec_choice.json", mode="branching"
        )
        bad = copy.deepcopy(witness)
        bad["mode"] = "weak"
        errors = verify_witness(impl, spec, bad)
        self.assertTrue(errors)

    def test_witness_json_roundtrip(self):
        impl, spec, witness = self._witness(
            "examples/impl_deadlock.json", "examples/spec_progress.json"
        )
        text = json.dumps(witness)
        self.assertEqual(verify_witness(impl, spec, json.loads(text)), [])


if __name__ == "__main__":
    unittest.main()
