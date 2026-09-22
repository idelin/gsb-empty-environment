"""Unit tests for core automaton features."""
from __future__ import annotations

import unittest

from statree import (
    Automaton,
    CancellationError,
    CountdownCancel,
    Dag,
    LimitExceeded,
    Limits,
    Rule,
    RuleError,
    StructuralCycleError,
    parse_condition,
)
from statree.model import Signature


def shared_dag_spec():
    alphabet = ("a", "b")
    nodes = [
        (0, "a", [], {"color": "red", "v": 2}),
        (1, "a", [], {"color": "blue", "v": 1}),
        (2, "b", [0, 0]),
        (3, "b", [2, 1]),
        (4, "b", [2, 0]),
    ]
    rules = [
        {"name": "leaf", "state": "qL", "label": "a", "child_states": []},
        {"name": "red", "state": "qR", "label": "a", "child_states": [],
         "condition": ("color", "==", "red")},
        {"name": "vge2", "state": "qV", "label": "a", "child_states": [],
         "condition": ("v", ">=", 2)},
        {"name": "pair", "state": "qP", "label": "b",
         "child_states": [{"qR"}, {"qV"}]},
        {"name": "top", "state": "qT", "label": "b",
         "child_states": [{"qP"}, {"qL"}]},
        {"name": "top-r", "state": "qT", "label": "b",
         "child_states": [{"qP"}, {"qR"}], "condition": None},
    ]
    return alphabet, nodes, rules


class ConditionsTests(unittest.TestCase):
    def test_operators_and_combinators(self):
        attrs = {"v": 3, "color": "red"}
        self.assertTrue(parse_condition(("v", ">=", 3)).holds(attrs))
        self.assertFalse(parse_condition(("v", "<", 3)).holds(attrs))
        self.assertTrue(parse_condition(("color", "in", ["red"])).holds(attrs))
        self.assertTrue(parse_condition(("has", "v")).holds(attrs))
        self.assertFalse(parse_condition({"not": ("has", "v")}).holds(attrs))
        self.assertTrue(parse_condition(
            {"all": [("v", ">", 1), {"any": [("color", "==", "red")]}]}
        ).holds(attrs))
        self.assertFalse(parse_condition(("missing", "==", 1)).holds(attrs))


class DagTests(unittest.TestCase):
    def test_structural_cycles_rejected_with_witness(self):
        with self.assertRaises(StructuralCycleError) as ctx:
            Dag.from_adjacency("fg", [(0, "f", [1]), (1, "g", [0])])
        self.assertEqual(ctx.exception.path, (0, 1, 0))

    def test_self_loop_and_dangling_refs(self):
        with self.assertRaises(StructuralCycleError):
            Dag.from_adjacency("f", [(0, "f", [0])])
        with self.assertRaises(Exception):
            Dag.from_adjacency("f", [(0, "f", [9])])

    def test_alphabet_enforced_and_hash_cons_shares(self):
        dag = Dag.from_adjacency("ab", [(0, "a", []), (1, "a", [])])
        with self.assertRaises(Exception):
            dag.intern("z")
        nid = dag.intern("a", (), {"x": 1})
        again = dag.intern("a", (), {"x": 1})
        self.assertEqual(nid, again)
        other = dag.intern("a", (), {"x": 2})
        self.assertNotEqual(nid, other)


class RuleTests(unittest.TestCase):
    def test_arity_is_child_count(self):
        self.assertEqual(Rule("q", "f", [{"a"}]).arity, 1)
        with self.assertRaises(RuleError):
            Rule("q", "f", [set()])

    def test_signature_filter(self):
        sig = Signature.parse({"states": ["qT"], "label": "b", "arity": 2})
        self.assertTrue(sig.matches("b", 2))
        self.assertFalse(sig.matches("b", 1))


class AutomatonTests(unittest.TestCase):
    def setUp(self):
        alphabet, nodes, rules = shared_dag_spec()
        self.auto = Automaton(
            alphabet, nodes, rules, roots={"left": 3, "right": 4},
            signatures=[{"states": {"qT"}}],
        )

    def test_states_and_accepting(self):
        self.assertEqual(self.auto.states(0), frozenset({"qL", "qR", "qV"}))
        self.assertEqual(self.auto.states(1), frozenset({"qL"}))
        self.assertEqual(self.auto.states(2), frozenset({"qP"}))
        self.assertEqual(self.auto.states(3), frozenset({"qT"}))
        self.assertEqual(self.auto.accepting()["left"], frozenset({"qT"}))

    def test_shared_node_evaluated_once(self):
        # One bottom-up pass: firings == number of rule candidates across nodes
        # but a re-evaluation of a shared child never happens.
        self.auto.evaluate_all()
        attempts = self.auto.stats.firings
        self.assertEqual(attempts, sum(
            len(self.auto.rules.candidates(self.auto.dag.node(i)))
            for i in self.auto.dag.ids()
        ))

    def test_occurrence_paths_distinguish_positions(self):
        ev = self.auto.explain(2, "qP")
        self.assertEqual(len(ev), 1)
        kids = ev[0].children
        self.assertEqual([k.node_id for k in kids], [0, 0])
        self.assertEqual([k.path_str for k in kids], ["$[0]", "$[1]"])

    def test_snapshot_is_immutable_history(self):
        snap = self.auto.snapshot(1)
        old_accept = snap.accepting_states("left")
        self.auto.edit_leaf("left", (1,), label="a", attrs={"color": "red", "v": 2})
        self.auto.commit()
        # history unchanged
        self.assertEqual(snap.accepting_states("left"), old_accept)
        self.assertEqual(snap.states(1), frozenset({"qL"}))
        self.assertEqual(snap.dag.node(1).attr_map()["color"], "blue")
        # old snapshot still explains
        self.assertTrue(snap.explain(snap.roots["left"], "qT"))

    def test_add_and_delete_rule_support_retraction(self):
        self.assertNotIn("qBlue", self.auto.states(1))
        self.auto.add_rule({"name": "blue", "state": "qBlue", "label": "a",
                            "child_states": [],
                            "condition": ("color", "==", "blue")})
        self.assertIn("qBlue", self.auto.states(1))
        self.auto.delete_rule("blue")
        self.assertNotIn("qBlue", self.auto.states(1))

        # deleting pair rule retracts qP and the qT states needing it
        self.auto.delete_rule("pair")
        self.assertEqual(self.auto.states(2), frozenset())
        self.assertEqual(self.auto.states(3), frozenset())
        self.assertEqual(self.auto.states(4), frozenset())

    def test_alternate_justification_keeps_state(self):
        self.auto.add_rule({"name": "pair2", "state": "qP", "label": "b",
                            "child_states": [{"qR"}, {"qV"}]})
        self.auto.delete_rule("pair")
        self.assertIn("qP", self.auto.states(2))
        self.auto.delete_rule("pair2")
        self.assertNotIn("qP", self.auto.states(2))

    def test_deep_edit_touches_only_spine(self):
        before = {i: self.auto.states(i) for i in self.auto.dag.ids()}
        report = self.auto.edit_leaf("left", (1,), label="a",
                                     attrs={"color": "red", "v": 2})
        spine = set(report.rebuilt)
        for nid in before:
            if nid not in spine:
                self.assertEqual(self.auto.states(nid), before[nid])

    def test_limits_and_cancellation(self):
        alphabet, nodes, rules = shared_dag_spec()
        auto = Automaton(alphabet, nodes, rules, roots={"left": 3})
        auto.limits = Limits(max_firings=1)
        with self.assertRaises(LimitExceeded):
            auto.evaluate_all()
        auto.limits = Limits()
        with self.assertRaises(CancellationError):
            auto.evaluate_all(token=CountdownCancel(1))
        auto.evaluate_all()
        self.assertTrue(auto.states(3))


class FixpointSemanticsTests(unittest.TestCase):
    def test_ungrounded_cycle_cannot_self_support(self):
        auto = Automaton(
            "xy",
            [(0, "x", []), (1, "y", [0])],
            [
                {"name": "cy", "state": "qY", "label": "y",
                 "child_states": [{"qX"}]},
                {"name": "cx", "state": "qX", "label": "x",
                 "child_states": [{"qY"}]},
            ],
            roots={"c": 1},
        )
        self.assertEqual(auto.states(0), frozenset())
        self.assertEqual(auto.states(1), frozenset())
        auto.add_rule({"name": "xb", "state": "qX", "label": "x",
                       "child_states": []})
        self.assertEqual(auto.states(0), frozenset({"qX"}))
        self.assertEqual(auto.states(1), frozenset({"qY"}))
        auto.delete_rule("xb")
        self.assertEqual(auto.states(0), frozenset())
        self.assertEqual(auto.states(1), frozenset())


if __name__ == "__main__":
    unittest.main(verbosity=2)
