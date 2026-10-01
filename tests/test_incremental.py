import unittest

from dfa_min.core import DEAD_STATE, build_dfa, minimize
from dfa_min.incremental import IncrementalMinimizer
from tests.test_core import SAMPLE, sample_dfa


def edited_sample(e0_target=None, a1_target=None):
    import copy
    data = copy.deepcopy(SAMPLE)
    if e0_target is not None:
        data["transitions"].setdefault("E", {})["0"] = e0_target
    if a1_target is not None:
        data["transitions"]["A"]["1"] = a1_target
    return build_dfa(data["alphabet"], data["states"], data["transitions"],
                     data["start"], data["accepting"])


class TestIncrementalRepair(unittest.TestCase):
    def setUp(self):
        self.inc = IncrementalMinimizer(sample_dfa())

    def test_initial_partition_matches_full_minimization(self):
        cert = minimize(sample_dfa())
        self.assertEqual(self.inc.quotient(), cert.quotient)
        self.assertEqual(self.inc.mapping(), cert.mapping)

    def test_merge_repair(self):
        report = self.inc.modify([("E", "0", "E")])
        mapping = self.inc.mapping()
        self.assertEqual(mapping["A"], mapping["E"])
        self.assertNotEqual(mapping["A"], mapping["B"])
        self.assertEqual(report.removed_unreachable, (DEAD_STATE,))
        self.assertTrue(report.merges)
        self.assertFalse(report.splits)
        self.assertGreater(report.touched_pairs, 0)
        cert = minimize(edited_sample(e0_target="E"))
        self.assertEqual(self.inc.quotient(), cert.quotient)

    def test_split_repair(self):
        self.inc.modify([("E", "0", "E")])
        report = self.inc.modify([("A", "1", "A")])
        mapping = self.inc.mapping()
        self.assertNotEqual(mapping["A"], mapping["E"])
        self.assertEqual(mapping["B"], mapping["C"])
        self.assertTrue(report.splits)
        cert = minimize(edited_sample(e0_target="E", a1_target="A"))
        self.assertEqual(self.inc.quotient(), cert.quotient)

    def test_merge_then_merge_back(self):
        self.inc.modify([("E", "0", "E")])
        self.inc.modify([("A", "1", "A")])
        report = self.inc.modify([("A", "1", "B")])
        mapping = self.inc.mapping()
        self.assertEqual(mapping["A"], mapping["E"])
        self.assertTrue(report.merges)
        cert = minimize(edited_sample(e0_target="E"))
        self.assertEqual(self.inc.quotient(), cert.quotient)

    def test_noop_edit_touches_nothing(self):
        report = self.inc.modify([("A", "0", "E")])
        self.assertEqual(report.touched_pairs, 0)
        self.assertFalse(report.splits)
        self.assertFalse(report.merges)

    def test_touched_pairs_bounded_by_state_pairs(self):
        report = self.inc.modify([("E", "0", "E")])
        n = len(self.inc.dfa.states)
        self.assertLessEqual(report.touched_pairs, n * n)

    def test_unknown_edit_rejected(self):
        with self.assertRaises(ValueError):
            self.inc.modify([("A", "2", "B")])
        with self.assertRaises(ValueError):
            self.inc.modify([("A", "0", "ZZ")])

    def test_random_edits_match_from_scratch(self):
        import random
        rng = random.Random(20261001)
        dfa = sample_dfa()
        inc = IncrementalMinimizer(dfa)
        current = {s: dict(row) for s, row in
                   __import__("dfa_min.core", fromlist=["prepare"]).prepare(dfa).transitions.items()}
        states = sorted(current)
        for _ in range(25):
            s = rng.choice(states)
            a = rng.choice(dfa.alphabet)
            t = rng.choice(states)
            if s not in inc.dfa.states or t not in inc.dfa.states:
                continue
            inc.modify([(s, a, t)])
            current = {x: dict(row) for x, row in inc.dfa.transitions.items()}
            rebuilt = build_dfa(dfa.alphabet, inc.dfa.states, current,
                                inc.dfa.start, inc.dfa.accepting)
            cert = minimize(rebuilt)
            self.assertEqual(inc.quotient(), cert.quotient)


if __name__ == "__main__":
    unittest.main()
