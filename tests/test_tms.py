import unittest

from minitms import TruthMaintenanceSystem
from minitms.tms import ReplayError


def build_main_case():
    tms = TruthMaintenanceSystem()
    tms.add_rule("r1", ("A", "B"), "C")   # A & B => C
    tms.add_rule("r2", ("D",), "C")       # D => C (alternative)
    tms.assert_fact("fA", "A")
    tms.assert_fact("fB", "B")
    tms.assert_fact("fD", "D")
    return tms


class TestConjunctionAndAlternatives(unittest.TestCase):
    def test_conjunctive_premises_required(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("r1", ("A", "B"), "C")
        tms.assert_fact("fA", "A")
        self.assertFalse(tms.believed("C"))
        tms.assert_fact("fB", "B")
        self.assertTrue(tms.believed("C"))
        self.assertEqual(tms.supports("C"), [frozenset({"fA", "fB"})])

    def test_two_alternative_supports(self):
        tms = build_main_case()
        self.assertEqual(
            tms.supports("C"),
            sorted([frozenset({"fA", "fB"}), frozenset({"fD"})],
                   key=lambda e: (len(e), tuple(sorted(e)))),
        )
        self.assertEqual(tms.support_count("C"), 2)

    def test_retract_b_but_d_keeps_c(self):
        tms = build_main_case()
        tms.retract_fact("fB")
        self.assertTrue(tms.believed("C"))
        self.assertEqual(tms.supports("C"), [frozenset({"fD"})])
        self.assertEqual(tms.support_count("C"), 1)
        self.assertFalse(tms.step("r1").active)
        self.assertTrue(tms.step("r2").active)

    def test_retract_d_then_c_withdrawn(self):
        tms = build_main_case()
        tms.retract_fact("fB")
        tms.retract_fact("fD")
        self.assertFalse(tms.believed("C"))
        self.assertEqual(tms.supports("C"), [])
        self.assertFalse(tms.step("r2").active)

    def test_re_add_fact_restores_conjunctive_support(self):
        tms = build_main_case()
        tms.retract_fact("fB")
        tms.retract_fact("fD")
        self.assertFalse(tms.believed("C"))
        tms.assert_fact("fB", "B")
        self.assertTrue(tms.believed("C"))
        self.assertEqual(tms.supports("C"), [frozenset({"fA", "fB"})])
        self.assertTrue(tms.step("r1").active)


class TestDuplicateFacts(unittest.TestCase):
    def test_repeated_assert_same_id_is_noop(self):
        tms = build_main_case()
        before = tms.snapshot()
        returned = tms.assert_fact("fB", "B")
        self.assertEqual(returned.version, 1)
        self.assertEqual(tms.fact("fB").version, 1)
        self.assertEqual(tms.support_count("C"), 2)
        self.assertEqual(tms.snapshot()["labels"], before["labels"])

    def test_duplicate_atom_with_distinct_ids_not_fake_bump(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("r1", ("A", "B"), "C")
        tms.assert_fact("fA1", "A")
        tms.assert_fact("fA2", "A")  # second independent source of A
        tms.assert_fact("fB", "B")
        # Two alternative grounds exist, but the minimal label keeps exactly
        # the two minimal environments — no multiplicity inflation.
        self.assertEqual(
            sorted(tms.supports("C"), key=lambda e: tuple(sorted(e))),
            sorted([frozenset({"fA1", "fB"}), frozenset({"fA2", "fB"})],
                   key=lambda e: tuple(sorted(e))),
        )
        self.assertEqual(tms.support_count("C"), 2)
        # asserting fA2 again adds nothing
        tms.assert_fact("fA2", "A")
        self.assertEqual(tms.support_count("C"), 2)

    def test_retract_one_duplicate_id_leaves_other(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("r1", ("A",), "C")
        tms.assert_fact("f1", "A")
        tms.assert_fact("f2", "A")
        self.assertEqual(tms.support_count("C"), 2)
        tms.retract_fact("f1")
        self.assertTrue(tms.believed("C"))
        self.assertEqual(tms.supports("C"), [frozenset({"f2"})])
        tms.retract_fact("f2")
        self.assertFalse(tms.believed("C"))


class TestUngroundedCycles(unittest.TestCase):
    def test_pure_cycle_supports_nothing(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("xy", ("X",), "Y")
        tms.add_rule("yx", ("Y",), "X")
        self.assertFalse(tms.believed("X"))
        self.assertFalse(tms.believed("Y"))
        self.assertFalse(tms.step("xy").active)
        self.assertFalse(tms.step("yx").active)

    def test_cycle_grounded_then_ground_removed(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("xy", ("X",), "Y")
        tms.add_rule("yx", ("Y",), "X")
        tms.assert_fact("seed", "X")
        self.assertTrue(tms.believed("X"))
        self.assertTrue(tms.believed("Y"))
        tms.retract_fact("seed")
        self.assertFalse(tms.believed("X"))
        self.assertFalse(tms.believed("Y"))
        self.assertFalse(tms.step("xy").active)
        self.assertFalse(tms.step("yx").active)

    def test_self_loop_alone_is_ungrounded(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("self", ("P",), "P")
        self.assertFalse(tms.believed("P"))
        tms.assert_fact("fp", "P")
        self.assertTrue(tms.believed("P"))


class TestStableIdsAndVersions(unittest.TestCase):
    def test_versions_start_and_increment(self):
        tms = TruthMaintenanceSystem()
        r = tms.add_rule("r1", ("A",), "C")
        self.assertEqual(r.version, 1)
        self.assertEqual(tms.step("r1").version, 0)
        self.assertFalse(tms.step("r1").active)
        tms.assert_fact("fA", "A")
        step = tms.step("r1")
        self.assertTrue(step.active)
        self.assertEqual(step.version, 1)
        tms.retract_fact("fA")
        step = tms.step("r1")
        self.assertFalse(step.active)
        self.assertEqual(step.version, 2)
        tms.assert_fact("fA", "A")
        self.assertTrue(tms.step("r1").active)
        self.assertEqual(tms.step("r1").version, 3)

    def test_fact_versions_on_retract_and_reassert(self):
        tms = TruthMaintenanceSystem()
        tms.assert_fact("fA", "A")
        self.assertEqual(tms.fact("fA").version, 1)
        tms.retract_fact("fA")
        self.assertEqual(tms.fact("fA").version, 2)
        tms.assert_fact("fA", "A")
        self.assertEqual(tms.fact("fA").version, 3)
        # redundant retraction changes nothing
        tms.retract_fact("fA")
        tms.retract_fact("fA")
        self.assertEqual(tms.fact("fA").version, 4)

    def test_step_stable_id_matches_rule_id(self):
        tms = build_main_case()
        self.assertEqual(set(tms.steps()), {"r1", "r2"})

    def test_duplicate_rule_id_rejected(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("r", ("A",), "B")
        with self.assertRaises(ValueError):
            tms.add_rule("r", ("C",), "D")


class TestMinimalSupports(unittest.TestCase):
    def test_superset_environment_pruned(self):
        # A is ground directly and derivable with an extra fact via a rule;
        # the direct singleton environment must be the only minimal one.
        tms = TruthMaintenanceSystem()
        tms.add_rule("r_ax", ("A", "X"), "A")
        tms.assert_fact("fA", "A")
        tms.assert_fact("fX", "X")
        self.assertEqual(tms.supports("A"), [frozenset({"fA"})])

    def test_axiom_rule_holds_with_empty_environment(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("ax", (), "Q")
        self.assertTrue(tms.believed("Q"))
        self.assertEqual(tms.supports("Q"), [frozenset()])
        self.assertEqual(tms.support_count("Q"), 1)

    def test_chained_derivation(self):
        tms = TruthMaintenanceSystem()
        tms.add_rule("r1", ("A",), "B")
        tms.add_rule("r2", ("B", "C"), "D")
        tms.assert_fact("fA", "A")
        tms.assert_fact("fC", "C")
        self.assertEqual(tms.supports("D"), [frozenset({"fA", "fC"})])
        tms.retract_fact("fC")
        self.assertFalse(tms.believed("D"))
        self.assertTrue(tms.believed("B"))


class TestExplain(unittest.TestCase):
    def test_explain_tree_structure(self):
        tms = build_main_case()
        ex = tms.explain("C")
        self.assertTrue(ex["believed"])
        self.assertEqual(len(ex["supports"]), 2)
        by_env = {tuple(s["env"]): s["tree"] for s in ex["supports"]}
        tree_d = by_env[("fD",)]
        self.assertEqual(tree_d["kind"], "rule")
        self.assertEqual(tree_d["rule_id"], "r2")
        self.assertEqual(tree_d["premises"][0]["kind"], "premise")

    def test_explain_unknown_atom(self):
        tms = build_main_case()
        ex = tms.explain("ZZ")
        self.assertFalse(ex["believed"])
        self.assertEqual(ex["supports"], [])


class TestReplay(unittest.TestCase):
    def test_snapshot_identical_after_replay(self):
        tms = build_main_case()
        tms.retract_fact("fB")
        tms.retract_fact("fD")
        tms.assert_fact("fD", "D")
        evidence = tms.to_evidence()
        replayed = TruthMaintenanceSystem.from_evidence(evidence)
        self.assertEqual(replayed.snapshot(), tms.snapshot())
        self.assertEqual(replayed.to_evidence(), tms.to_evidence())

    def test_replay_log_records_versions(self):
        tms = TruthMaintenanceSystem()
        tms.assert_fact("fA", "A")
        tms.retract_fact("fA")
        events = tms.to_evidence()["events"]
        self.assertEqual(events[0]["version"], 1)
        self.assertEqual(events[1]["version"], 2)

    def test_replay_bad_version_raises(self):
        with self.assertRaises(ReplayError):
            TruthMaintenanceSystem.from_evidence({"version": 9, "events": []})

    def test_replay_idempotent_operations_not_logged(self):
        tms = TruthMaintenanceSystem()
        tms.assert_fact("fA", "A")
        tms.assert_fact("fA", "A")  # no-op
        tms.retract_fact("fA")
        tms.retract_fact("fA")      # no-op
        events = tms.to_evidence()["events"]
        self.assertEqual([e["op"] for e in events],
                         ["assert_fact", "retract_fact"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
