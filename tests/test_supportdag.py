import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))

from supportdag import (  # noqa: E402
    ANY,
    NOT,
    Fact,
    KnowledgeBase,
    Rule,
    VerificationError,
    brute_force_minimal_supports,
    entails,
    min_cardinality_supports,
    verify_evidence,
    wfs_true,
)
from supportdag.model import body_dnf  # noqa: E402


def pull_all(kb, goal, budget=10000):
    view = kb.query(goal, budget)
    return {frozenset(s.facts) for s in view.supports}, view


class DNFSemanticsTests(unittest.TestCase):
    def test_conjunction_alternatives_and_negation_normalize(self):
        rule = Rule("r", "g", ("x", ANY(("y", NOT("z")), ("w",))))
        alts = body_dnf(rule.body)
        self.assertEqual(
            alts,
            frozenset({
                frozenset({"x", "y", ("not", "z")}),
                frozenset({"x", "w"}),
            }),
        )

    def test_contradictory_alternative_is_dropped(self):
        # Only branch is contradictory; combined with y it yields nothing.
        rule = Rule("r", "g", (ANY(("x", NOT("x"))), "y"))
        self.assertEqual(rule.alternatives, frozenset())
        # A non-contradictory sibling branch inside the same ANY survives.
        rule2 = Rule("r2", "g", (ANY(("x", NOT("x")), "y"),))
        self.assertEqual(rule2.alternatives, frozenset({frozenset({"y"})}))


class WFSTests(unittest.TestCase):
    def test_rootless_positive_cycle_proves_nothing(self):
        rules = [Rule("a", "a", ("a",))]
        self.assertFalse(entails(set(), rules, "a"))

    def test_mutual_rootless_cycle_proves_nothing(self):
        rules = [Rule("r1", "p", ("q",)), Rule("r2", "q", ("p",))]
        self.assertFalse(entails(set(), rules, "p"))
        self.assertFalse(entails(set(), rules, "q"))

    def test_grounded_chain_through_cycle_shape_works(self):
        rules = [Rule("r1", "p", ("q",)), Rule("r2", "q", ("f",))]
        self.assertTrue(entails({"f"}, rules, "p"))

    def test_negative_premise_basic(self):
        rules = [Rule("r", "g", ("b", NOT("a")))]
        self.assertTrue(entails({"b"}, rules, "g"))
        self.assertFalse(entails({"a", "b"}, rules, "g"))

    def test_negative_self_loop_is_undefined_not_true(self):
        rules = [Rule("r", "b", (NOT("b"),))]
        self.assertFalse(entails(set(), rules, "b"))
        self.assertTrue(entails({"b"}, rules, "b"))


class DominanceAndReappearanceTests(unittest.TestCase):
    def _build(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_fact("B", "b"),
            kb.add_fact("C", "c"),
            kb.add_rule(Rule("r_ab", "g", ("a", "b"))),
            kb.add_rule(Rule("r_a", "g", ("a",))),
            kb.add_rule(Rule("r_c", "h", ("c",))),
        )
        return kb

    def test_A_dominates_A_B_but_not_C(self):
        kb = self._build()
        g_sets, _ = pull_all(kb, "g")
        h_sets, _ = pull_all(kb, "h")
        self.assertEqual(g_sets, {frozenset({"A"})})
        self.assertEqual(h_sets, {frozenset({"C"})})

    def test_pruned_alternative_reappears_after_retraction(self):
        kb = self._build()
        # Initially {A} dominates the {A,B} proof, so {B} alone proves nothing.
        self.assertEqual(pull_all(kb, "g")[0], {frozenset({"A"})})
        # Remove A: r_a dies; g still not provable by B alone here, so build a
        # scenario where B becomes a genuine proof once A is retracted, using a
        # negative premise (this is exactly non-monotone reappearance).
        kb2 = KnowledgeBase()
        kb2.commit(
            kb2.add_fact("FB", "b"),
            kb2.add_rule(Rule("r_b", "g", ("b", NOT("a")))),
            kb2.add_rule(Rule("r_a", "g", ("a",))),
        )
        self.assertEqual(pull_all(kb2, "g")[0], {frozenset({"FB"})})
        kb2.commit(kb2.add_fact("FA", "a"))
        self.assertEqual(pull_all(kb2, "g")[0], {frozenset({"FA"})})
        report = kb2.commit(kb2.retract_fact("FA"))
        self.assertEqual(pull_all(kb2, "g")[0], {frozenset({"FB"})})
        self.assertIn("g", report.revived_goals)

    def test_negative_premise_fails_when_fact_added(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("FB", "b"),
            kb.add_rule(Rule("r", "g", ("b", NOT("a")))),
        )
        self.assertEqual(pull_all(kb, "g")[0], {frozenset({"FB"})})
        kb.commit(kb.add_fact("FA", "a"))
        self.assertEqual(pull_all(kb, "g")[0], set())


class SharedSubproofTests(unittest.TestCase):
    def test_goals_share_subproofs_but_not_results(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("F", "f"),
            kb.add_rule(Rule("rx", "x", ("f",))),
            kb.add_rule(Rule("rg", "g", ("x",))),
            kb.add_rule(Rule("rh", "h", ("x",))),
        )
        g_sets, g_view = pull_all(kb, "g")
        h_sets, h_view = pull_all(kb, "h")
        self.assertEqual(g_sets, {frozenset({"F"})})
        self.assertEqual(h_sets, {frozenset({"F"})})
        # Independent acceptance state per goal.
        self.assertEqual(len(g_view.supports), 1)
        self.assertEqual(len(h_view.supports), 1)
        self.assertIsNot(g_view.supports[0], h_view.supports[0])
        # Shared sub-step id for x, proving DAG reuse without result leakage.
        g_steps = {s.step_id for s in g_view.supports[0].steps}
        h_steps = {s.step_id for s in h_view.supports[0].steps}
        self.assertTrue(any(step_id.startswith("rule:rx:") for step_id in g_steps))
        self.assertTrue(any(step_id.startswith("rule:rx:") for step_id in h_steps))

    def test_result_does_not_leak_between_unrelated_goals(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_rule(Rule("rg", "g", ("a",))),
            kb.add_rule(Rule("rh", "h", ("zzz",))),
        )
        self.assertEqual(pull_all(kb, "g")[0], {frozenset({"A"})})
        self.assertEqual(pull_all(kb, "h")[0], set())


class ExhaustiveSmallGraphTests(unittest.TestCase):
    def test_engine_matches_brute_force_oracle(self):
        # Positive-only small graph: exhaustive oracle and engine agree on the
        # full inclusion-minimal family under self-contained semantics.
        universe = {"F1": "a", "F2": "b", "F3": "c", "F4": "d"}
        rules = [
            Rule("r1", "g", ("a", ANY("b", "c"))),
            Rule("r3", "x", ("a", "b")),
            Rule("r4", "g", ("x", "c")),
            Rule("r5", "g", ("d",)),
        ]
        kb = KnowledgeBase()
        changes = [kb.add_fact(fid, atom) for fid, atom in universe.items()]
        changes += [kb.add_rule(r) for r in rules]
        kb.commit(*changes)
        engine_sets, view = pull_all(kb, "g", budget=20000)
        self.assertTrue(view.complete)
        oracle = brute_force_minimal_supports(universe, rules, "g")
        self.assertEqual(engine_sets, oracle)

    def test_inclusion_minimal_differs_from_minimum_cardinality(self):
        universe = {"F1": "a", "F2": "b", "F3": "c"}
        rules = [
            Rule("r1", "g", (ANY("a", ("b", "c")),)),
        ]
        minimal = brute_force_minimal_supports(universe, rules, "g")
        self.assertEqual(
            minimal,
            {frozenset({"F1"}), frozenset({"F2", "F3"})},
        )
        mincard = min_cardinality_supports(minimal)
        self.assertEqual(mincard, (frozenset({"F1"}),))

    def test_cost_order_is_nondecreasing(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a", cost=10),
            kb.add_fact("B", "b", cost=1),
            kb.add_fact("C", "c", cost=1),
            kb.add_rule(Rule("r", "g", (ANY("a", ("b", "c")),))),
        )
        view = kb.query("g", budget=1000)
        costs = [s.cost for s in view.supports]
        self.assertEqual(costs, sorted(costs))
        # Cheapest set {B,C} (2) precedes {A} (10).
        self.assertEqual([set(s.facts) for s in view.supports][0], {"B", "C"})


class BudgetPauseResumeTests(unittest.TestCase):
    def _chain_kb(self, n=6):
        kb = KnowledgeBase()
        changes = []
        for i in range(n):
            changes.append(kb.add_fact(f"F{i}", f"f{i}"))
        # g :- ANY(f0, f1, ..., fn) gives n+1 independent supports.
        changes.append(kb.add_rule(Rule("rg", "g", tuple([ANY(*[f"f{i}" for i in range(n)])]))))
        kb.commit(*changes)
        return kb

    def test_over_budget_is_truncated_not_complete(self):
        kb = self._chain_kb(6)
        view = kb.query("g", budget=2)
        self.assertTrue(view.last.truncated)
        self.assertFalse(view.last.complete)
        self.assertEqual(view.status, "truncated")

    def test_resume_until_complete(self):
        kb = self._chain_kb(6)
        view = kb.query("g", budget=2)
        seen = set(s.support_id for s in view.supports)
        for _ in range(10):
            result = view.pull(2)
            seen.update(s.support_id for s in result.supports)
            if result.complete:
                break
        self.assertTrue(view.complete)
        self.assertEqual(len(seen), 6)

    def test_no_false_completeness_claim(self):
        kb = self._chain_kb(8)
        view = kb.query("g", budget=1)
        self.assertFalse(view.last.complete)
        self.assertTrue(view.last.budget_exhausted)


class VersioningAndIncrementalTests(unittest.TestCase):
    def test_fact_version_change_revises_dependents(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_rule(Rule("rg", "g", ("a",))),
            kb.add_rule(Rule("rh", "h", ("z",))),
        )
        kb.query("g", 100)
        kb.query("h", 100)
        report = kb.commit(kb.update_fact_version("A", 2))
        self.assertEqual(report.affected_goals, ("g",))
        self.assertEqual(kb.goal("h").revision, kb.revision)

    def test_rule_version_change_revises_only_dependent_goals(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_rule(Rule("rg", "g", ("a",))),
            kb.add_rule(Rule("rh", "h", ("a",))),
        )
        kb.query("g", 100)
        kb.query("h", 100)
        report = kb.commit(
            kb.update_rule(Rule("rg", "g", ("a", "never"), version=2))
        )
        self.assertIn("g", report.affected_goals)
        self.assertNotIn("h", report.affected_goals)

    def test_revision_monotone_and_stable_ids(self):
        kb = KnowledgeBase()
        kb.commit(kb.add_fact("A", "a"), kb.add_rule(Rule("r", "g", ("a",))))
        s1 = kb.query("g", 100).supports[0]
        kb.commit(kb.update_fact_version("A", 2))
        s2 = kb.goal("g").supports[0]
        self.assertNotEqual(s1.steps[0].step_id, s2.steps[0].step_id)
        self.assertNotEqual(s1.support_id, s2.support_id)


class EvidenceAndVerifierTests(unittest.TestCase):
    def test_evidence_is_replayable_and_sufficient_irredundant(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_fact("B", "b"),
            kb.add_rule(Rule("r1", "x", ("a",))),
            kb.add_rule(Rule("r2", "g", ("x", "b"))),
        )
        support = kb.query("g", 100).supports[0]
        bundle = support.to_bundle(kb.snapshot())
        result = verify_evidence(bundle, live_kb=kb.to_json())
        self.assertTrue(result["ok"])
        self.assertEqual(result["goal"], "g")

    def test_verifier_rejects_redundant_fact(self):
        # Hand-build a bundle whose fact list contains an extra, unused fact.
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_fact("X", "x"),
            kb.add_rule(Rule("r", "g", ("a",))),
        )
        support = kb.query("g", 100).supports[0]
        bundle = support.to_bundle(kb.snapshot())
        # Inject redundant fact X into the evidence.
        bundle["facts"].append({"fid": "X", "atom": "x", "version": 1})
        bundle["support_id"] = "tampered"
        with self.assertRaises(VerificationError):
            verify_evidence(bundle)

    def test_verifier_rejects_tampered_step(self):
        kb = KnowledgeBase()
        kb.commit(kb.add_fact("A", "a"), kb.add_rule(Rule("r", "g", ("a",))))
        bundle = kb.query("g", 100).supports[0].to_bundle(kb.snapshot())
        bundle["steps"][-1]["step_id"] = "rule:r:v1:deadbeefdeadbeef"
        with self.assertRaises(VerificationError):
            verify_evidence(bundle)

    def test_verifier_detects_version_drift(self):
        kb = KnowledgeBase()
        kb.commit(kb.add_fact("A", "a"), kb.add_rule(Rule("r", "g", ("a",))))
        bundle = kb.query("g", 100).supports[0].to_bundle(kb.snapshot())
        kb.commit(kb.update_fact_version("A", 2))
        with self.assertRaises(VerificationError):
            verify_evidence(bundle, live_kb=kb.to_json())

    def test_kb_roundtrip_replays_identically(self):
        kb = KnowledgeBase()
        kb.commit(
            kb.add_fact("A", "a"),
            kb.add_fact("B", "b"),
            kb.add_rule(Rule("r", "g", ("a", ANY("b", NOT("z"))))),
        )
        data = kb.to_json()
        kb2 = KnowledgeBase.from_json(data)
        s1 = kb.query("g", 100).supports[0]
        s2 = kb2.query("g", 100).supports[0]
        self.assertEqual(s1.support_id, s2.support_id)
        self.assertEqual(
            [s.step_id for s in s1.steps], [s.step_id for s in s2.steps]
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)


class RandomizedExhaustiveTests(unittest.TestCase):
    def test_many_random_positive_graphs_match_oracle(self):
        import itertools
        import random

        rng = random.Random(20260925)
        atoms = ["a", "b", "c", "e"]
        universe = {f"F{i}": atom for i, atom in enumerate(atoms)}

        rule_templates = [
            lambda h, i: Rule(f"r{h}{i}", h, (atoms[i % len(atoms)],)),
            lambda h, i: Rule(
                f"r{h}{i}", h,
                (atoms[i % len(atoms)], atoms[(i + 1) % len(atoms)])
            ),
            lambda h, i: Rule(
                f"r{h}{i}", h,
                (ANY(atoms[i % len(atoms)],
                     atoms[(i + 2) % len(atoms)]),),
            ),
        ]

        for trial in range(40):
            rules = []
            heads = ["g", "x", "y"]
            for h_index, head in enumerate(heads):
                picks = rng.sample(range(6), k=rng.randint(1, 3))
                for j, pick in enumerate(picks):
                    tpl = rule_templates[pick % len(rule_templates)]
                    rules.append(tpl(head, trial * 10 + h_index * 10 + j))
            kb = KnowledgeBase()
            changes = [kb.add_fact(fid, atom) for fid, atom in universe.items()]
            changes += [kb.add_rule(rule) for rule in rules]
            kb.commit(*changes)
            engine_sets, view = pull_all(kb, "g", budget=100000)
            oracle = brute_force_minimal_supports(universe, rules, "g")
            self.assertEqual(
                engine_sets, oracle,
                msg=f"trial {trial} mismatch\nrules="
                    f"{[(r.rid, r.head, r.body) for r in rules]}",
            )
            self.assertTrue(view.complete)


if __name__ == "__main__":
    unittest.main(verbosity=2)
