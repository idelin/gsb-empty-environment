import random
import unittest

from tracecheck import (
    TAU,
    BehaviorComparator,
    StateMachine,
    Verdict,
    brute_force_distinguishing_word,
    compare,
    enumerate_visible_traces,
)


def chain_machine(states, labels, version="v1"):
    """Helper: a simple chain s0 -labels[0]-> s1 -labels[1]-> ..."""
    transitions = [
        (states[i], labels[i], states[i + 1]) for i in range(len(labels))
    ]
    return StateMachine(states, states[0], transitions, version=version)


class RenamingTests(unittest.TestCase):
    def test_pure_renaming_is_equivalent(self):
        m1 = StateMachine(
            {"idle", "busy", "done"},
            "idle",
            [("idle", "start", "busy"), ("busy", "finish", "done")],
            version="v1",
        )
        m2 = StateMachine(
            {"s0", "s1", "s2"},
            "s0",
            [("s0", "start", "s1"), ("s1", "finish", "s2")],
            version="v2",
        )
        result = compare(m1, m2, bound=8)
        self.assertIs(result.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)
        self.assertIsNone(result.counterexample)


class TraceDiffTests(unittest.TestCase):
    def test_added_trace_is_reported(self):
        old = chain_machine(["a", "b"], ["ping"], version="v1")
        new = StateMachine(
            {"a", "b"},
            "a",
            [("a", "ping", "b"), ("a", "reset", "a")],
            version="v2",
        )
        result = compare(old, new, bound=4)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        cex = result.counterexample
        self.assertEqual(cex.word, ("reset",))
        self.assertFalse(cex.trace_of_first)
        self.assertEqual(cex.first_subset, frozenset())
        self.assertEqual(cex.second_subset, frozenset({"a"}))
        self.assertTrue(cex.replay(old, new))

    def test_removed_trace_is_reported(self):
        old = StateMachine(
            {"a", "b"},
            "a",
            [("a", "ping", "b"), ("a", "reset", "a")],
            version="v1",
        )
        new = chain_machine(["a", "b"], ["ping"], version="v2")
        result = compare(old, new, bound=4)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        cex = result.counterexample
        self.assertEqual(cex.word, ("reset",))
        self.assertTrue(cex.trace_of_first)
        self.assertTrue(cex.replay(old, new))

    def test_shortest_distinguishing_word_is_returned(self):
        m1 = StateMachine(
            {"s0", "s1", "s2", "s3"},
            "s0",
            [
                ("s0", "a", "s1"),
                ("s1", "b", "s2"),
                ("s2", "c", "s3"),
                ("s0", "x", "s3"),
            ],
        )
        m2 = StateMachine(
            {"t0", "t1", "t2", "t3"},
            "t0",
            [
                ("t0", "a", "t1"),
                ("t1", "b", "t2"),
                ("t2", "d", "t3"),
                ("t0", "x", "t3"),
            ],
        )
        result = compare(m1, m2, bound=5)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        word = result.counterexample.word
        self.assertEqual(len(word), 3)
        self.assertIn(word, [("a", "b", "c"), ("a", "b", "d")])

    def test_beyond_bound_stays_bounded_conclusion(self):
        m1 = chain_machine(["s0", "s1", "s2", "s3"], ["a", "b", "c"])
        m2 = chain_machine(["t0", "t1", "t2", "t3"], ["a", "b", "d"])
        bounded = compare(m1, m2, bound=2)
        self.assertIs(bounded.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)
        deeper = compare(m1, m2, bound=3)
        self.assertIs(deeper.verdict, Verdict.DIFFERENT)
        self.assertEqual(len(deeper.counterexample.word), 3)


class TauAndEmptyTests(unittest.TestCase):
    def test_tau_loop_is_invisible(self):
        looping = StateMachine(
            {"s0", "s1", "s2"},
            "s0",
            [
                ("s0", TAU, "s1"),
                ("s1", TAU, "s0"),
                ("s1", TAU, "s1"),
                ("s0", "a", "s2"),
            ],
        )
        plain = StateMachine({"t0", "t1"}, "t0", [("t0", "a", "t1")])
        result = compare(looping, plain, bound=4)
        self.assertIs(result.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)

    def test_tau_only_machines_have_only_empty_trace(self):
        m1 = StateMachine({"s0", "s1"}, "s0", [("s0", TAU, "s1")])
        m2 = StateMachine({"t0"}, "t0", [])
        self.assertEqual(enumerate_visible_traces(m1, 3), {()})
        result = compare(m1, m2, bound=3)
        self.assertIs(result.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)

    def test_empty_trace_distinguishes_empty_machine(self):
        empty = StateMachine(set(), None, [])
        nonempty = StateMachine({"s0"}, "s0", [])
        result = compare(empty, nonempty, bound=3)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        cex = result.counterexample
        self.assertEqual(cex.word, ())
        self.assertFalse(cex.trace_of_first)
        self.assertTrue(cex.replay(empty, nonempty))

    def test_two_empty_machines_are_equivalent(self):
        e1 = StateMachine(set(), None, [])
        e2 = StateMachine(set(), None, [])
        result = compare(e1, e2, bound=3)
        self.assertIs(result.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)


class NondeterminismTests(unittest.TestCase):
    def test_subset_semantics_not_state_pairing(self):
        # No one-to-one state matching with the deterministic machine can
        # work, yet the visible trace sets are identical.
        nondet = StateMachine(
            {"s0", "s1", "s2", "s3", "s4"},
            "s0",
            [
                ("s0", "a", "s1"),
                ("s0", "a", "s2"),
                ("s1", "b", "s3"),
                ("s2", "b", "s4"),
            ],
        )
        det = StateMachine(
            {"t0", "t1", "t2"},
            "t0",
            [("t0", "a", "t1"), ("t1", "b", "t2")],
        )
        result = compare(nondet, det, bound=4)
        self.assertIs(result.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)

    def test_nondeterministic_branch_difference_found(self):
        nondet = StateMachine(
            {"s0", "s1", "s2"},
            "s0",
            [("s0", "a", "s1"), ("s0", "a", "s2"), ("s1", "b", "s2")],
        )
        det = StateMachine({"t0", "t1"}, "t0", [("t0", "a", "t1")])
        result = compare(nondet, det, bound=4)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.counterexample.word, ("a", "b"))
        self.assertTrue(result.counterexample.trace_of_first)


class ReplayTests(unittest.TestCase):
    def test_counterexample_replay_and_tamper_detection(self):
        m1 = chain_machine(["s0", "s1"], ["a"])
        m2 = chain_machine(["t0", "t1", "t2"], ["a", "a"])
        result = compare(m1, m2, bound=3)
        cex = result.counterexample
        self.assertEqual(cex.word, ("a", "a"))
        self.assertTrue(cex.replay(m1, m2))
        tampered = type(cex)(
            ("a",), cex.trace_of_first, cex.first_subset, cex.second_subset
        )
        self.assertFalse(tampered.replay(m1, m2))


class UnknownTests(unittest.TestCase):
    def test_budget_exhaustion_stays_pending(self):
        m1 = chain_machine(["s0", "s1", "s2"], ["a", "b"])
        m2 = chain_machine(["t0", "t1", "t2"], ["a", "b"])
        result = compare(m1, m2, bound=5, max_steps=1)
        self.assertIs(result.verdict, Verdict.UNKNOWN)
        self.assertIsNone(result.counterexample)

    def test_unknown_results_are_not_cached(self):
        comparator = BehaviorComparator()
        m1 = chain_machine(["s0", "s1"], ["a"])
        m2 = chain_machine(["t0", "t1"], ["a"])
        comparator.compare(m1, m2, bound=3, max_steps=0)
        comparator.compare(m1, m2, bound=3, max_steps=0)
        self.assertEqual(comparator.hits, 0)
        self.assertEqual(comparator.misses, 2)


class CacheTests(unittest.TestCase):
    def setUp(self):
        self.comparator = BehaviorComparator()
        self.old = chain_machine(["a", "b"], ["ping"], version="v1")
        self.same = chain_machine(["x", "y"], ["ping"], version="v2")

    def test_cache_hit_on_repeat(self):
        self.comparator.compare(self.old, self.same, bound=3)
        self.comparator.compare(self.old, self.same, bound=3)
        self.assertEqual(self.comparator.hits, 1)
        self.assertEqual(self.comparator.misses, 1)

    def test_version_update_invalidates_cache(self):
        first = self.comparator.compare(self.old, self.same, bound=3)
        self.assertIs(first.verdict, Verdict.EQUIVALENT_UP_TO_BOUND)
        changed = self.same.updated(
            version="v3",
            transitions=[("x", "ping", "y"), ("x", "reset", "x")],
        )
        second = self.comparator.compare(self.old, changed, bound=3)
        self.assertIs(second.verdict, Verdict.DIFFERENT)
        self.assertEqual(second.counterexample.word, ("reset",))
        self.assertEqual(self.comparator.misses, 2)
        self.assertEqual(self.comparator.hits, 0)

    def test_structural_change_without_version_bump_still_invalidates(self):
        self.comparator.compare(self.old, self.same, bound=3)
        sneaky = StateMachine(
            {"x", "y"},
            "x",
            [("x", "ping", "y"), ("y", "extra", "x")],
            version="v2",
        )
        result = self.comparator.compare(self.old, sneaky, bound=3)
        self.assertIs(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(self.comparator.misses, 2)


class CrossCheckTests(unittest.TestCase):
    def random_machine(self, rng, n_states, labels, version):
        states = [f"s{i}" for i in range(n_states)]
        transitions = []
        for src in states:
            for label in labels + [TAU]:
                if rng.random() < 0.35:
                    transitions.append((src, label, rng.choice(states)))
        return StateMachine(states, states[0], transitions, version=version)

    def test_product_search_matches_brute_force(self):
        rng = random.Random(20260928)
        for trial in range(60):
            m1 = self.random_machine(rng, rng.randint(1, 4), ["a", "b"], "v1")
            m2 = self.random_machine(rng, rng.randint(1, 4), ["a", "b"], "v2")
            bound = 3
            result = compare(m1, m2, bound=bound)
            brute_word = brute_force_distinguishing_word(m1, m2, bound)
            if brute_word is None:
                self.assertIs(
                    result.verdict,
                    Verdict.EQUIVALENT_UP_TO_BOUND,
                    msg=f"trial {trial}",
                )
            else:
                self.assertIs(result.verdict, Verdict.DIFFERENT, msg=f"trial {trial}")
                cex = result.counterexample
                self.assertEqual(len(cex.word), len(brute_word), msg=f"trial {trial}")
                self.assertEqual(
                    m1.accepts(cex.word), cex.trace_of_first, msg=f"trial {trial}"
                )
                self.assertNotEqual(
                    m1.accepts(cex.word), m2.accepts(cex.word), msg=f"trial {trial}"
                )
                self.assertTrue(cex.replay(m1, m2), msg=f"trial {trial}")


if __name__ == "__main__":
    unittest.main()
