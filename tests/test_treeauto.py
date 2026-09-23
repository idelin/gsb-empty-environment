"""Unittest suite for the ordered tree automaton package (stdlib only)."""

from __future__ import annotations

import itertools
import json
import random
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT))

from treeauto import (Alphabet, AlphabetError, AttrCondition, Automaton,
                      CancelToken, CancelledError, Limits, MaxCountConstraint,
                      MutexConstraint, Node, NoProofError, ResourceLimitError,
                      Rule, RuleError, Session, StructuralCycleError,
                      check_expectation, evaluate, postorder_unique,
                      validate_dag, Counterexample)
from samples.replay_samples import build_automaton, build_tree, run_sample


def mini_automaton():
    """Tiny automaton for exhaustive tests."""
    alphabet = Alphabet.of({"a": {0}, "b": {0}, "f": {1}, "g": {2}})
    rules = [
        Rule.of("ra", "a", [], "p"),
        Rule.of("rb", "b", [], "q"),
        Rule.of("rfp", "f", ["p"], "q"),
        Rule.of("rfq", "f", ["q"], "p"),
        Rule.of("rgpp", "g", ["p", "p"], "p"),
        Rule.of("rgqq", "g", ["q", "q"], "q"),
        Rule.of("rgpq", "g", ["p", "q"], "p"),
    ]
    return Automaton(alphabet, {"p", "q"}, rules, accepting={"p"})


def enumerate_trees(max_depth):
    leaves = [Node.make("a"), Node.make("b")]
    prev = list(leaves)
    for _ in range(max_depth):
        cur = list(leaves)
        cur += [Node.make("f", [t]) for t in prev]
        cur += [Node.make("g", [s, t])
                for s, t in itertools.product(prev, repeat=2)]
        prev = cur
    return prev


class TestBasics(unittest.TestCase):
    def test_acceptance_and_proof(self):
        automaton = build_automaton()
        root, _ = build_tree()
        result = evaluate(automaton, root)
        self.assertTrue(result.accepted)
        self.assertEqual(result.root_states, frozenset({"q_bool"}))
        proof = result.proof("q_bool")
        self.assertEqual(proof.path, ())
        self.assertEqual(proof.rule_id, "r_and")
        rendered = proof.render()
        self.assertIn("q_bool @ path=()", rendered)
        self.assertIn("via r_plus_int", rendered)

    def test_no_proof_raises(self):
        automaton = build_automaton()
        root, _ = build_tree()
        result = evaluate(automaton, root)
        with self.assertRaises(NoProofError):
            result.proof("q_nat")  # root never carries q_nat

    def test_alphabet_validation(self):
        automaton = build_automaton()
        bad = Node.make("mystery", [Node.make("num", attrs={"value": 1})])
        with self.assertRaises(AlphabetError):
            evaluate(automaton, bad)

    def test_structural_cycle_forbidden(self):
        a = Node.make("a")
        b = Node.make("f", [a])
        object.__setattr__(a, "children", (b,))  # maliciously crafted cycle
        with self.assertRaises(StructuralCycleError):
            validate_dag(a)

    def test_arity_not_mixed(self):
        alphabet = Alphabet.of({"a": {0}, "f": {1, 2}})
        rules = [Rule.of("ra", "a", [], "q"),
                 Rule("rf1", "f", 1, ("q",), "r")]
        automaton = Automaton(alphabet, {"q", "r"}, rules, accepting={"r"})
        unary = Node.make("f", [Node.make("a")])
        binary = Node.make("f", [Node.make("a"), Node.make("a")])
        self.assertEqual(evaluate(automaton, unary).root_states,
                         frozenset({"r"}))
        # same label, different arity: rule rf1 must not fire
        self.assertEqual(evaluate(automaton, binary).root_states, frozenset())

    def test_rule_arity_must_be_in_alphabet(self):
        alphabet = Alphabet.of({"f": {1}})
        with self.assertRaises(RuleError):
            Automaton(alphabet, {"q"},
                      [Rule("bad", "f", 2, ("q", "q"), "q")], {"q"})

    def test_attr_conditions(self):
        alphabet = Alphabet.of({"num": {0}})
        rules = [Rule.of("small", "num", [], "q_small",
                         [AttrCondition("value", "le", 10)]),
                 Rule.of("any", "num", [], "q_num")]
        automaton = Automaton(alphabet, {"q_small", "q_num"}, rules,
                              accepting={"q_small"})
        small = evaluate(automaton, Node.make("num", attrs={"value": 3}))
        big = evaluate(automaton, Node.make("num", attrs={"value": 99}))
        self.assertTrue(small.accepted)
        self.assertFalse(big.accepted)
        self.assertEqual(big.root_states, frozenset({"q_num"}))


class TestSharingAndEvidence(unittest.TestCase):
    def test_shared_subtree_computed_once_two_evidences(self):
        automaton = build_automaton()
        root, shared = build_tree()
        result = evaluate(automaton, root)
        # one cache entry despite two parent positions
        per_node = result.stats.node_evaluations
        occurrences = result.occurrences(shared)
        self.assertEqual(len(occurrences), 2)
        # a node occurring N times costs the same as occurring once:
        ten = root.children[0].children[1]
        self.assertEqual(per_node[shared], per_node[ten])
        # but the two occurrences give distinct path evidence
        proof_a = result.proof("q_nat", path=occurrences[0])
        proof_b = result.proof("q_nat", path=occurrences[1])
        self.assertNotEqual(proof_a.path, proof_b.path)
        self.assertEqual(proof_a.node, proof_b.node)  # same cached subtree

    def test_proof_paths_track_occurrence(self):
        automaton = build_automaton()
        root, shared = build_tree()
        result = evaluate(automaton, root)
        plus_proof = result.proof("q_int", path=(0, 0))
        child_paths = sorted(c.path for c in plus_proof.children)
        self.assertEqual(child_paths, [(0, 0, 0), (0, 0, 1)])


class TestFixpointAndCycles(unittest.TestCase):
    def test_rule_cycle_converges(self):
        alphabet = Alphabet.of({"a": {0}, "f": {1}})
        rules = [Rule.of("base", "a", [], "q1"),
                 Rule.of("c1", "f", ["q1"], "q2"),
                 Rule.of("c2", "f", ["q2"], "q1")]
        automaton = Automaton(alphabet, {"q1", "q2"}, rules, accepting={"q1"})
        tree = Node.make("f", [Node.make("f", [Node.make("a")])])
        result = evaluate(automaton, tree)
        self.assertTrue(result.accepted)  # a:q1, f:q2, f^2:q1 at root
        deeper = Node.make("f", [tree])
        self.assertEqual(evaluate(automaton, deeper).root_states,
                         frozenset({"q2"}))  # the cycle alternates
        self.assertLessEqual(result.stats.iterations, 3)

    def test_no_root_support_not_self_sustaining(self):
        alphabet = Alphabet.of({"a": {0}, "f": {1}})
        # q1/q2 only support each other; no base rule ever fires
        rules = [Rule.of("c1", "f", ["q1"], "q2"),
                 Rule.of("c2", "f", ["q2"], "q1")]
        automaton = Automaton(alphabet, {"q1", "q2"}, rules, accepting={"q1"})
        tree = Node.make("f", [Node.make("f", [Node.make("a")])])
        result = evaluate(automaton, tree)
        self.assertEqual(result.facts_view(),
                         {n: frozenset() for n in postorder_unique(tree)})
        self.assertFalse(result.accepted)

    def test_resource_limits(self):
        automaton = build_automaton()
        root, _ = build_tree()
        with self.assertRaises(ResourceLimitError):
            evaluate(automaton, root, limits=Limits(max_iterations=1))
        with self.assertRaises(ResourceLimitError):
            evaluate(automaton, root, limits=Limits(max_steps=2))
        with self.assertRaises(ResourceLimitError):
            evaluate(automaton, root, limits=Limits(max_facts=1))

    def test_cancellation(self):
        automaton = build_automaton()
        root, _ = build_tree()
        token = CancelToken()

        def hook(step, node, rule):
            if step >= 3:
                token.cancel()

        with self.assertRaises(CancelledError):
            evaluate(automaton, root, token=token, on_step=hook)


class TestConstraints(unittest.TestCase):
    def test_mutex_constraint(self):
        alphabet = Alphabet.of({"num": {0}})
        rules = [Rule.of("r1", "num", [], "qa"),
                 Rule.of("r2", "num", [], "qb")]
        automaton = Automaton(alphabet, {"qa", "qb"}, rules,
                              accepting={"qa"},
                              constraints=[MutexConstraint("qa", "qb")])
        result = evaluate(automaton, Node.make("num"))
        self.assertEqual(len(result.violations), 1)
        self.assertFalse(result.accepted)  # violation blocks acceptance

    def test_max_count_constraint(self):
        alphabet = Alphabet.of({"a": {0}, "g": {2}})
        rules = [Rule.of("ra", "a", [], "p"),
                 Rule.of("rg", "g", ["p", "p"], "p")]
        automaton = Automaton(alphabet, {"p"}, rules, accepting={"p"},
                              constraints=[MaxCountConstraint("p", 1)])
        tree = Node.make("g", [Node.make("a"), Node.make("a")])
        result = evaluate(automaton, tree)
        # shared leaf counts once: 2 distinct holders (a, g) > maximum 1
        self.assertEqual(len(result.violations), 1)
        self.assertFalse(result.accepted)


class TestIncremental(unittest.TestCase):
    def test_deep_leaf_edit_only_ancestors(self):
        automaton = build_automaton()
        root, _ = build_tree()
        session = Session(automaton, root)
        before = session.snapshot()
        report = session.edit((1, 1, 1), label="num", attrs={"value": 1})
        # spine length == depth + 1, nothing else recomputed
        self.assertEqual(report.recomputed_count, 4)
        stats = session.snapshot().stats
        self.assertEqual(sum(stats.node_evaluations.values()) > 0, True)
        self.assertEqual(len(stats.node_evaluations), 4)
        self.assertTrue(report.accepted_before)
        self.assertFalse(report.accepted_after)

    def test_old_snapshot_still_queryable(self):
        automaton = build_automaton()
        root, _ = build_tree()
        session = Session(automaton, root)
        snap0 = session.snapshot(0)
        session.edit((1, 1, 1), label="num", attrs={"value": 1})
        self.assertEqual(snap0.states_at((1, 1, 1)), frozenset({"q_bool"}))
        self.assertTrue(snap0.accepted)
        self.assertFalse(session.snapshot().accepted)

    def test_replace_everywhere_updates_all_parents(self):
        automaton = build_automaton()
        root, shared = build_tree()
        session = Session(automaton, root)
        report = session.replace_everywhere(
            shared, Node.make("num", attrs={"value": -5}))
        # shared node + plus + lt + root recomputed (chain untouched)
        self.assertEqual(report.recomputed_count, 4)
        plus = session.root.children[0].children[0]
        states = session.snapshot().states_of(plus)
        self.assertNotIn("q_nat", states)       # -5 fails the ge-0 condition
        self.assertIn("q_int", states)
        self.assertTrue(session.snapshot().accepted)  # lt only needs q_int

    def test_delete_rule_revokes_unsupported_states(self):
        automaton = build_automaton()
        root, shared = build_tree()
        session = Session(automaton, root)
        report = session.delete_rules(["r_num_nat"])
        revoked = set(report.revoked)
        self.assertIn((shared, "q_nat"), revoked)
        # cascade: plus lost q_nat because its children lost it
        plus = root.children[0].children[0]
        self.assertIn((plus, "q_nat"), revoked)
        self.assertNotIn("q_nat", session.snapshot().states_of(shared))
        # q_int support is intact
        self.assertIn("q_int", session.snapshot().states_of(shared))

    def test_add_rules_extends_states(self):
        alphabet = Alphabet.of({"a": {0}})
        automaton = Automaton(alphabet, {"p", "q"},
                              [Rule.of("ra", "a", [], "p")], {"q"})
        session = Session(automaton, Node.make("a"))
        self.assertFalse(session.snapshot().accepted)
        session.add_rules([Rule.of("lift", "a", [], "q")])
        self.assertTrue(session.snapshot().accepted)


class TestExhaustiveVsIncremental(unittest.TestCase):
    def test_small_trees_exhaustive(self):
        automaton = mini_automaton()
        rng = random.Random(20260923)
        trees = enumerate_trees(max_depth=2)
        self.assertEqual(len(trees), 74)
        for index, tree in enumerate(trees):
            fresh = evaluate(automaton, tree)
            session = Session(automaton, tree)
            self.assertEqual(session.snapshot().facts_view(),
                             fresh.facts_view(), f"initial run differs {index}")
            # two deterministic pseudo-random leaf edits
            for _ in range(2):
                paths = [p for n, ps in session.snapshot().occurrences().items()
                         for p in ps]
                path = rng.choice(paths)
                node = session.snapshot().node_at(path)
                if node.label in ("a", "b"):
                    session.edit(path, label="b" if node.label == "a" else "a")
                else:
                    session.edit(path, label="f" if node.label == "g" else "g",
                                 children=node.children[:1]
                                 if node.label == "g"
                                 else (node.children[0], node.children[0]))
                current = session.snapshot()
                reference = evaluate(automaton, session.root)
                self.assertEqual(current.facts_view(), reference.facts_view(),
                                 f"incremental mismatch after edit, tree {index}")
                self.assertEqual(current.accepted, reference.accepted)
            # snapshot 0 still describes the original tree
            self.assertEqual(session.snapshot(0).facts_view(),
                             fresh.facts_view())


class TestCounterexample(unittest.TestCase):
    def test_roundtrip_and_replay(self):
        automaton = build_automaton()
        root, _ = build_tree()
        session = Session(automaton, root)
        session.edit((1, 1, 1), label="num", attrs={"value": 1})
        cx = check_expectation(session.automaton, session.root, True,
                               note="edited tree should stay accepted")
        self.assertIsNotNone(cx)
        payload = cx.to_json()
        json.loads(payload)  # valid JSON
        clone = Counterexample.from_json(payload)
        replay = clone.replay()
        self.assertTrue(replay.confirmed)
        self.assertFalse(replay.actual_accepted)

    def test_no_counterexample_when_expectation_holds(self):
        automaton = build_automaton()
        root, _ = build_tree()
        self.assertIsNone(check_expectation(automaton, root, True))

    def test_runner_subprocess_independent(self):
        automaton = build_automaton()
        root, _ = build_tree()
        session = Session(automaton, root)
        session.edit((1, 1, 1), label="num", attrs={"value": 1})
        cx = check_expectation(session.automaton, session.root, True)
        with tempfile.TemporaryDirectory() as tmp:
            case = Path(tmp) / "case.json"
            case.write_text(cx.to_json(), encoding="utf-8")
            proc = subprocess.run(
                [sys.executable, "-m", "samples.counterexample_runner",
                 str(case)],
                cwd=REPO_ROOT, capture_output=True, text=True)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("CONFIRMED", proc.stdout)


EXPECTED_TRANSCRIPT = """\
STEP 01 build: root_states=[q_bool] accepted=True
STEP 02 shared-subtree: occurrences=[(0, 0, 0), (0, 0, 1)] cache_evaluations=4
STEP 03 path-evidence: first=(0, 0, 0) second=(0, 0, 1) distinct=True
STEP 04 deep-leaf-edit: path=(1, 1, 1) recomputed=4 accepted True->False
STEP 05 old-snapshot: states_at(1, 1, 1)=[q_bool] accepted=True (still queryable)
STEP 06 delete-rule r_num_nat: revoked=["num{'value': 1}:q_nat", "num{'value': 10}:q_nat", "num{'value': 3}:q_nat", 'plus{}:q_nat']
STEP 07 counterexample: expected=True actual=False confirmed=True
STEP 08 final: root_states=[] accepted=False snapshots=3"""


class TestReplayableSample(unittest.TestCase):
    def test_transcript_is_replayable(self):
        self.assertEqual(run_sample(), EXPECTED_TRANSCRIPT)
        self.assertEqual(run_sample(), EXPECTED_TRANSCRIPT)  # deterministic


if __name__ == "__main__":
    unittest.main()
