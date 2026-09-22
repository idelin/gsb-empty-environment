"""Small-tree exhaustive cross-check between incremental and from-scratch runs.

A genuinely independent reference evaluator is written here (it does not use
the package's fixpoint routine): it repeats synchronous rounds starting from
the empty state set until stability.  Every rule add/delete and every leaf edit
performed incrementally by :class:`statree.Automaton` must agree with this
reference on the full state table.
"""
from __future__ import annotations

import unittest

from statree import Automaton
from statree.model import Rule


def reference_evaluate(dag, rule_specs):
    """Independent least-fixpoint evaluator (no package engine reuse)."""
    rules = [Rule.parse(r) for r in rule_specs]
    states = {nid: set() for nid in dag.ids()}

    def node_supports(rule, nid):
        node = dag.node(nid)
        if node.label != rule.label or node.arity != rule.arity:
            return False
        if not rule.condition.holds(node.attr_map()):
            return False
        for pos, wanted in enumerate(rule.child_states):
            if not (wanted & states[node.children[pos]]):
                return False
        return True

    while True:
        snapshot = {nid: set(s) for nid, s in states.items()}
        for nid in dag.ids():
            for rule in rules:
                if rule.state in states[nid]:
                    continue
                if node_supports(rule, nid):
                    states[nid].add(rule.state)
        if states == snapshot:
            return {nid: frozenset(s) for nid, s in states.items()}


class ExhaustiveTests(unittest.TestCase):
    ALPHABET = ("a", "k", "b", "x", "y")

    RULES = [
        {"name": "a", "state": "qA", "label": "a", "child_states": []},
        {"name": "a-pos", "state": "qAP", "label": "a", "child_states": [],
         "condition": ("v", ">=", 1)},
        {"name": "k1", "state": "qK1", "label": "k",
         "child_states": [{"qA"}]},
        {"name": "k2", "state": "qK2", "label": "k",
         "child_states": [{"qA"}, {"qA"}]},
        {"name": "b1", "state": "qB1", "label": "b",
         "child_states": [{"qA"}]},
        {"name": "b2", "state": "qB2", "label": "b",
         "child_states": [{"qA"}, {"qA"}]},
        {"name": "b-mix", "state": "qBMix", "label": "b",
         "child_states": [{"qK1"}, {"qAP"}]},
        {"name": "cyc-y", "state": "qY", "label": "y",
         "child_states": [{"qX"}]},
        {"name": "cyc-x", "state": "qX", "label": "x",
         "child_states": [{"qY"}]},
        {"name": "x-base", "state": "qX", "label": "x", "child_states": []},
        {"name": "a-extra", "state": "qAE", "label": "a", "child_states": [],
         "condition": ("color", "in", ["red", "blue"])},
    ]

    def build_universe(self):
        dag_nodes = []
        seq = [0]

        def add(label, children=(), attrs=None):
            nid = seq[0]
            seq[0] += 1
            dag_nodes.append((nid, label, tuple(children), attrs))
            return nid

        # Deterministic growth, two rounds, content-addressed sharing happens
        # automatically inside the automaton DAG.
        a0 = add("a", attrs={"v": 0})
        a1 = add("a", attrs={"v": 1, "color": "red"})
        a2 = add("a", attrs={"v": 2, "color": "blue"})
        leaves = [a0, a1, a2]
        unary: list[int] = []
        binary: list[int] = []
        for leaf in leaves:
            unary.append(add("k", [leaf]))
            unary.append(add("b", [leaf]))
        for left in leaves:
            for right in leaves:
                binary.append(add("b", [left, right]))
        for un in unary:
            for leaf in leaves:
                binary.append(add("b", [un, leaf]))
        xb = add("x")
        add("y", [xb])
        root = add("b", [binary[0], unary[0]])
        return dag_nodes, root

    def test_rule_operations_match_reference(self):
        raw, root = self.build_universe()
        steps = [
            ("add", "a"),
            ("add", "a-pos"),
            ("add", "k1"),
            ("add", "k2"),
            ("add", "b1"),
            ("add", "b2"),
            ("add", "b-mix"),
            ("add", "a-extra"),
            ("delete", "b2"),
            ("add", "b2"),
            ("delete", "a-pos"),
            ("add", "x-base"),
            ("add", "cyc-y"),
            ("add", "cyc-x"),
            ("delete", "x-base"),
            ("delete", "cyc-x"),
        ]
        live_rules: list[str] = []
        by_name = {spec["name"]: spec for spec in self.RULES}
        auto = Automaton(self.ALPHABET, raw, [], roots={"r": root})
        for kind, name in steps:
            spec = by_name[name]
            if kind == "add":
                auto.add_rule(spec)
                if name not in live_rules:
                    live_rules.append(name)
            else:
                auto.delete_rule(name)
                live_rules = [n for n in live_rules if n != name]
            expected = reference_evaluate(auto.dag, [by_name[n] for n in live_rules])
            for nid in auto.dag.ids():
                self.assertEqual(
                    auto.states(nid), expected[nid],
                    msg=f"{kind} {name}: node {nid} mismatch",
                )

    def test_fixpoint_mode_matches_topological_and_reference(self):
        raw, root = self.build_universe()
        auto = Automaton(self.ALPHABET, raw, self.RULES, roots={"r": root})
        topo = {nid: auto.states(nid) for nid in auto.dag.ids()}
        ref = reference_evaluate(auto.dag, self.RULES)
        auto.fixpoint_evaluate()
        fixed = {nid: auto.states(nid) for nid in auto.dag.ids()}
        self.assertEqual(topo, ref)
        self.assertEqual(fixed, ref)

    def test_leaf_edits_only_change_spine_and_match_reference(self):
        raw, root = self.build_universe()
        auto = Automaton(self.ALPHABET, raw, self.RULES, roots={"r": root})
        before = {nid: auto.states(nid) for nid in auto.dag.ids()}
        ref_before = reference_evaluate(auto.dag, self.RULES)
        self.assertEqual(before, ref_before)

        # Path to a deep leaf of the root: root[1] is a unary node -> its kid
        report = auto.edit_leaf("r", (1, 0), label="a",
                                attrs={"v": 9, "color": "red"})
        rebuilt = set(report.rebuilt)
        for nid in auto.dag.ids():
            if nid in rebuilt:
                continue
            self.assertEqual(auto.states(nid), before[nid])

        ref = reference_evaluate(auto.dag, self.RULES)
        for nid in auto.dag.ids():
            self.assertEqual(auto.states(nid), ref[nid])

    def test_editing_leaf_to_other_label(self):
        raw, root = self.build_universe()
        auto = Automaton(self.ALPHABET, raw, self.RULES, roots={"r": root})
        report = auto.edit_leaf("r", (1, 0), label="x")
        ref = reference_evaluate(auto.dag, self.RULES)
        for nid in auto.dag.ids():
            self.assertEqual(auto.states(nid), ref[nid])
        self.assertTrue(report.changed)


if __name__ == "__main__":
    unittest.main(verbosity=2)
