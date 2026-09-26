import random
import unittest

from tracecheck import (
    Determinizer,
    StateMachine,
    SubsetCache,
    Verdict,
    check_witness,
    compare_bounded,
    make_machine,
    reference_compare,
    replay,
    visible_words,
)


def chain_machine(actions, version="v1"):
    """Machine accepting exactly the prefixes of ``actions``."""
    states = [f"q{i}" for i in range(len(actions) + 1)]
    transitions = {
        (states[i], act, states[i + 1]) for i, act in enumerate(actions)
    }
    return make_machine(states, set(actions), transitions, states[0], version)


class RenamingTests(unittest.TestCase):
    def test_pure_renaming_is_not_a_difference(self):
        left = make_machine(
            {"idle", "busy"},
            {"start", "stop"},
            {("idle", "start", "busy"), ("busy", "stop", "idle")},
            "idle",
        )
        right = make_machine(
            {"S0", "S1"},
            {"start", "stop"},
            {("S0", "start", "S1"), ("S1", "stop", "S0")},
            "S0",
        )
        result = compare_bounded(left, right, bound=6)
        self.assertEqual(result.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)
        self.assertIsNone(result.witness)

    def test_renamed_machine_keeps_same_visible_words(self):
        left = chain_machine(["a", "b"])
        right = make_machine(
            {"x", "y", "z"}, {"a", "b"}, {("x", "a", "y"), ("y", "b", "z")}, "x"
        )
        self.assertEqual(visible_words(left, 3), visible_words(right, 3))


class DifferenceTests(unittest.TestCase):
    def test_shortest_distinguishing_sequence(self):
        left = chain_machine(["a", "b"])
        right = chain_machine(["a", "c"])
        result = compare_bounded(left, right, bound=4)
        self.assertEqual(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.witness, ("a", "b"))  # "a" alone is shared
        self.assertEqual(result.evidence.side, "left_only")

    def test_new_and_disappeared_traces_between_versions(self):
        old = make_machine(
            {"s0", "s1", "s2"},
            {"login", "reset", "logout"},
            {("s0", "login", "s1"), ("s1", "reset", "s0")},
            "s0",
            version="v1",
        )
        new = make_machine(
            {"s0", "s1", "s2"},
            {"login", "reset", "logout"},
            {("s0", "login", "s1"), ("s1", "logout", "s0")},
            "s0",
            version="v2",
        )
        result = compare_bounded(old, new, bound=4)
        self.assertEqual(result.verdict, Verdict.DIFFERENT)
        # shortest difference: ("login", "reset") disappeared / ("login", "logout") is new
        self.assertEqual(len(result.witness), 2)
        self.assertIn(result.witness, {("login", "reset"), ("login", "logout")})

    def test_nondeterminism_matches_its_determinisation(self):
        nfa = make_machine(
            {"s0", "s1", "s2", "s3"},
            {"a", "b"},
            {("s0", "a", "s1"), ("s0", "a", "s2"), ("s1", "b", "s3"), ("s2", "b", "s3")},
            "s0",
        )
        dfa = make_machine(
            {"t0", "t1", "t2"}, {"a", "b"}, {("t0", "a", "t1"), ("t1", "b", "t2")}, "t0"
        )
        result = compare_bounded(nfa, dfa, bound=4)
        self.assertEqual(result.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)

    def test_nondeterministic_branching_visible_in_traces(self):
        nfa = make_machine(
            {"s0", "s1", "s2"},
            {"a", "b", "c"},
            {
                ("s0", "a", "s1"),
                ("s0", "a", "s2"),
                ("s1", "b", "s1"),
                ("s1", "c", "s1"),
                ("s2", "b", "s2"),
                ("s2", "c", "s2"),
            },
            "s0",
        )
        det = make_machine(
            {"t0", "t1"},
            {"a", "b", "c"},
            {("t0", "a", "t1"), ("t1", "b", "t1"), ("t1", "c", "t1")},
            "t0",
        )
        result = compare_bounded(nfa, det, bound=5)
        self.assertEqual(result.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)


class TauAndEmptyTraceTests(unittest.TestCase):
    def test_tau_loop_terminates_and_empty_trace_shared(self):
        loop_only = make_machine(
            {"s0"}, {"a"}, {("s0", None, "s0")}, "s0"
        )
        no_transitions = make_machine({"t0"}, {"a"}, set(), "t0")
        result = compare_bounded(loop_only, no_transitions, bound=3)
        self.assertEqual(result.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)
        self.assertEqual(visible_words(loop_only, 3), {()})

    def test_tau_loop_plus_visible_step(self):
        with_a = make_machine(
            {"s0", "s1"}, {"a"}, {("s0", None, "s0"), ("s0", "a", "s1")}, "s0"
        )
        without_a = make_machine({"t0"}, {"a"}, {("t0", None, "t0")}, "t0")
        result = compare_bounded(with_a, without_a, bound=3)
        self.assertEqual(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.witness, ("a",))
        self.assertEqual(result.evidence.side, "left_only")

    def test_tau_chain_collapses_invisibly(self):
        # tau chain then "a" behaves exactly like a single "a"
        chained = make_machine(
            {"s0", "s1", "s2"},
            {"a"},
            {("s0", None, "s1"), ("s1", None, "s2"), ("s2", "a", "s0")},
            "s0",
        )
        direct = make_machine({"t0"}, {"a"}, {("t0", "a", "t0")}, "t0")
        result = compare_bounded(chained, direct, bound=4)
        self.assertEqual(result.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)


class BoundAndBudgetTests(unittest.TestCase):
    def test_difference_beyond_bound_stays_unclaimed(self):
        left = chain_machine(["a", "a", "a"])
        right = chain_machine(["a", "a"])
        within = compare_bounded(left, right, bound=2)
        self.assertEqual(within.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)
        beyond = compare_bounded(left, right, bound=3)
        self.assertEqual(beyond.verdict, Verdict.DIFFERENT)
        self.assertEqual(beyond.witness, ("a", "a", "a"))

    def test_exhausted_budget_gives_unknown(self):
        machine = chain_machine(["a", "b", "c"])
        result = compare_bounded(machine, machine, bound=3, max_pairs=0)
        self.assertEqual(result.verdict, Verdict.UNKNOWN)
        self.assertTrue(result.truncated)
        self.assertIsNone(result.witness)

    def test_tight_budget_still_finds_early_difference(self):
        left = chain_machine(["a"])
        right = chain_machine(["b"])
        result = compare_bounded(left, right, bound=2, max_pairs=1)
        self.assertEqual(result.verdict, Verdict.DIFFERENT)
        self.assertEqual(result.witness, ("a",))


class CacheTests(unittest.TestCase):
    def setUp(self):
        self.left = chain_machine(["a", "b"], version="v1")
        # same visible behaviour as left, different state names
        self.right_v1 = make_machine(
            {"r0", "r1", "r2"},
            {"a", "b"},
            {("r0", "a", "r1"), ("r1", "b", "r2")},
            "r0",
            version="v1",
        )

    def test_cache_populated_and_reused(self):
        cache = SubsetCache()
        compare_bounded(self.left, self.right_v1, bound=3, cache=cache)
        self.assertEqual(len(cache), 2)
        det = Determinizer(self.left, cache)
        self.assertTrue(det.cache_hit)

    def test_version_update_invalidates_cache(self):
        cache = SubsetCache()
        first = compare_bounded(self.left, self.right_v1, bound=3, cache=cache)
        self.assertEqual(first.verdict, Verdict.EQUIVALENT_WITHIN_BOUND)
        # update the right model: new version, extra visible trace
        right_v2 = make_machine(
            {"q0", "q1", "q2"},
            {"a", "b"},
            {("q0", "a", "q1"), ("q1", "b", "q2"), ("q1", "a", "q2")},
            "q0",
            version="v2",
        )
        det = Determinizer(right_v2, cache)
        self.assertFalse(det.cache_hit)  # stale v1 entry not served
        second = compare_bounded(self.left, right_v2, bound=3, cache=cache)
        self.assertEqual(second.verdict, Verdict.DIFFERENT)
        self.assertEqual(second.witness, ("a", "a"))
        self.assertEqual(second.evidence.side, "right_only")

    def test_same_version_different_content_misses_cache(self):
        cache = SubsetCache()
        compare_bounded(self.left, self.right_v1, bound=3, cache=cache)
        edited = chain_machine(["a", "c"], version="v1")  # same version, new content
        self.assertIsNone(cache.get(edited))  # fingerprint changed -> miss

    def test_explicit_version_invalidation(self):
        cache = SubsetCache()
        compare_bounded(self.left, self.right_v1, bound=3, cache=cache)
        removed = cache.invalidate_version("v1")
        self.assertEqual(removed, 2)
        self.assertEqual(len(cache), 0)

    def test_cached_and_uncached_results_agree(self):
        cache = SubsetCache()
        other = chain_machine(["a", "c"], version="v1")
        cold = compare_bounded(self.left, other, bound=3)
        compare_bounded(self.left, other, bound=3, cache=cache)
        warm = compare_bounded(self.left, other, bound=3, cache=cache)
        self.assertEqual(cold.verdict, warm.verdict)
        self.assertEqual(cold.witness, warm.witness)


class ReplayTests(unittest.TestCase):
    def test_witness_replays_on_both_sides(self):
        left = chain_machine(["a", "b"])
        right = chain_machine(["a", "c"])
        result = compare_bounded(left, right, bound=4)
        ev = result.evidence
        self.assertTrue(check_witness(left, right, ev.word, ev.side))
        # evidence trajectories: left ends non-empty, right ends empty
        self.assertTrue(ev.left_trajectory[-1])
        self.assertFalse(ev.right_trajectory[-1])
        # every prefix subset on the accepting side is non-empty
        for subset in ev.left_trajectory:
            self.assertTrue(subset)
        # trajectory length = |word| + 1 (empty prefix included)
        self.assertEqual(len(ev.left_trajectory), len(ev.word) + 1)
        # replaying by hand reproduces the stored evidence
        self.assertEqual(tuple(replay(left, ev.word)), ev.left_trajectory)
        self.assertEqual(tuple(replay(right, ev.word)), ev.right_trajectory)


class ModelValidationTests(unittest.TestCase):
    def test_unknown_state_rejected(self):
        with self.assertRaises(ValueError):
            make_machine({"s0"}, {"a"}, {("s0", "a", "s1")}, "s0")

    def test_unknown_initial_rejected(self):
        with self.assertRaises(ValueError):
            make_machine({"s0"}, {"a"}, set(), "s9")

    def test_label_outside_alphabet_rejected(self):
        with self.assertRaises(ValueError):
            make_machine({"s0", "s1"}, {"a"}, {("s0", "b", "s1")}, "s0")

    def test_model_is_finite_and_immutable(self):
        machine = chain_machine(["a"])
        self.assertIsInstance(machine, StateMachine)
        with self.assertRaises(AttributeError):
            machine.initial = "elsewhere"


class CrossCheckTests(unittest.TestCase):
    """Product search vs. brute-force word enumeration on random small models."""

    @staticmethod
    def random_machine(rng, n_states, alphabet, version):
        states = [f"s{i}" for i in range(n_states)]
        transitions = set()
        for src in states:
            for dst in states:
                if rng.random() < 0.10:
                    transitions.add((src, None, dst))  # tau
                for action in alphabet:
                    if rng.random() < 0.15:
                        transitions.add((src, action, dst))
        return make_machine(states, alphabet, transitions, states[0], version)

    def test_random_models_agree_with_reference(self):
        rng = random.Random(20260927)
        bound = 3
        for trial in range(150):
            left = self.random_machine(rng, rng.randint(1, 4), {"a", "b"}, "L")
            right = self.random_machine(rng, rng.randint(1, 4), {"a", "b"}, "R")
            result = compare_bounded(left, right, bound=bound)
            reference = reference_compare(left, right, bound)
            if reference is None:
                self.assertEqual(
                    result.verdict,
                    Verdict.EQUIVALENT_WITHIN_BOUND,
                    msg=f"trial {trial}",
                )
            else:
                word, side = reference
                self.assertEqual(result.verdict, Verdict.DIFFERENT, msg=f"trial {trial}")
                self.assertEqual(result.witness, word, msg=f"trial {trial}")
                self.assertEqual(result.evidence.side, side, msg=f"trial {trial}")
                self.assertTrue(check_witness(left, right, word, side))
                # minimality: every proper prefix is a trace of both sides
                for i in range(len(word)):
                    prefix = word[:i]
                    self.assertIn(prefix, visible_words(left, bound))
                    self.assertIn(prefix, visible_words(right, bound))


if __name__ == "__main__":
    unittest.main()
