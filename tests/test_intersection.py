"""unittest suite for the multi-DFA intersection toolkit.

Cross-checks the power-block machinery against exhaustive enumeration on
small finite domains, and validates every stated invariant:

* empty-string acceptance depends on *all* constraints;
* k-th selection uses exact counts only (modular mode refuses);
* unrank -> rank inverse on every accepted string;
* infinitude requires a reachable cycle that can reach acceptance;
* caches invalidate on DFA version bumps;
* enumeration checkpoints resume and never confuse budget with emptiness.
"""

import itertools
import random
import unittest

from dfa_intersection import (
    Checkpoint,
    CheckpointError,
    DFA,
    ExactCounter,
    ModCounter,
    UnsupportedModeError,
    accepts_all,
)

ALPHABET = ("a", "b")
MOD = 1_000_000_007


def random_dfa(rng, nstates):
    transitions = {}
    for state in range(nstates):
        for symbol in ALPHABET:
            if rng.random() < 0.8:
                transitions[(state, symbol)] = rng.randrange(nstates)
    accepting = {s for s in range(nstates) if rng.random() < 0.4}
    return DFA(ALPHABET, 0, accepting, transitions)


def brute_force(dfas, max_length):
    """All accepted strings grouped by length, in (length, lex) order."""
    accepted = {}
    for length in range(max_length + 1):
        strings = []
        for pieces in itertools.product(ALPHABET, repeat=length):
            text = "".join(pieces)
            if accepts_all(dfas, text):
                strings.append(text)
        accepted[length] = strings
    return accepted


class CountingTests(unittest.TestCase):
    def setUp(self):
        rng = random.Random(1234)
        self.cases = []
        for _ in range(12):
            dfas = [random_dfa(rng, rng.randrange(1, 5)),
                    random_dfa(rng, rng.randrange(1, 5))]
            self.cases.append((dfas, brute_force(dfas, 8)))

    def test_exact_counts_match_brute_force(self):
        for dfas, accepted in self.cases:
            counter = ExactCounter(dfas)
            for length, strings in accepted.items():
                self.assertEqual(counter.count_length(length), len(strings))

    def test_interval_counts_match_brute_force(self):
        for dfas, accepted in self.cases:
            counter = ExactCounter(dfas)
            for low in range(9):
                for high in range(low, 9):
                    expected = sum(len(accepted[n]) for n in range(low, high + 1))
                    self.assertEqual(counter.count_interval(low, high), expected)
            self.assertEqual(counter.count_interval(5, 4), 0)

    def test_empty_string_requires_all_constraints(self):
        accepts_empty = DFA("ab", 0, {0}, {(0, "a"): 0, (0, "b"): 0})
        rejects_empty = DFA("ab", 0, {1}, {
            (0, "a"): 1, (0, "b"): 1, (1, "a"): 1, (1, "b"): 1,
        })
        self.assertEqual(ExactCounter([accepts_empty]).count_length(0), 1)
        self.assertEqual(ExactCounter([rejects_empty]).count_length(0), 0)
        both = ExactCounter([accepts_empty, rejects_empty])
        self.assertEqual(both.count_length(0), 0)
        rejects_empty.set_accepting(0, True)
        self.assertEqual(both.count_length(0), 1)


class LexicographicTests(unittest.TestCase):
    def setUp(self):
        rng = random.Random(99)
        self.cases = []
        for _ in range(12):
            dfas = [random_dfa(rng, rng.randrange(1, 5)),
                    random_dfa(rng, rng.randrange(1, 5))]
            self.cases.append((dfas, brute_force(dfas, 7)))

    def test_kth_matches_sorted_brute_force(self):
        for dfas, accepted in self.cases:
            counter = ExactCounter(dfas)
            for length, strings in accepted.items():
                got = [counter.kth_string(length, k) for k in range(len(strings))]
                self.assertEqual(got, strings)

    def test_rank_is_inverse_of_kth(self):
        for dfas, accepted in self.cases:
            counter = ExactCounter(dfas)
            for length, strings in accepted.items():
                for k, text in enumerate(strings):
                    self.assertEqual(counter.rank(text), k)
                    self.assertEqual(counter.kth_string(length, counter.rank(text)), text)

    def test_rank_of_rejected_string(self):
        counter = ExactCounter([random_dfa(random.Random(1), 3)])
        self.assertIsNone(counter.rank("abababa"))
        with self.assertRaises(ValueError):
            counter.rank("ac")

    def test_kth_out_of_range(self):
        dfa = DFA("ab", 0, {0}, {(0, "a"): 0})
        counter = ExactCounter([dfa])
        self.assertEqual(counter.count_length(2), 1)
        with self.assertRaises(IndexError):
            counter.kth_string(2, 1)
        with self.assertRaises(ValueError):
            counter.kth_string(2, -1)

    def test_kth_in_interval_ordered_by_length_then_lex(self):
        for dfas, accepted in self.cases[:6]:
            counter = ExactCounter(dfas)
            ordered = []
            for low, high in ((0, 4), (2, 6), (3, 3)):
                ordered = [s for n in range(low, high + 1) for s in accepted[n]]
                for k, text in enumerate(ordered):
                    self.assertEqual(counter.kth_in_interval(low, high, k), text)
                with self.assertRaises(IndexError):
                    counter.kth_in_interval(low, high, len(ordered))


class ModularModeTests(unittest.TestCase):
    def setUp(self):
        rng = random.Random(7)
        self.cases = []
        for _ in range(8):
            dfas = [random_dfa(rng, 4), random_dfa(rng, 3)]
            self.cases.append((dfas, brute_force(dfas, 9)))

    def test_modular_matches_exact_reduced(self):
        for dfas, accepted in self.cases:
            exact = ExactCounter(dfas)
            mod = ModCounter(dfas, MOD)
            for length, strings in accepted.items():
                self.assertEqual(mod.count_length(length), len(strings) % MOD)
            self.assertEqual(
                mod.count_interval(2, 9), exact.count_interval(2, 9) % MOD
            )

    def test_modular_agrees_on_large_length_via_power_blocks(self):
        even_a = DFA("ab", 0, {0}, {
            (0, "a"): 1, (1, "a"): 0, (0, "b"): 0, (1, "b"): 1,
        })
        dfas = [even_a]
        exact = ExactCounter(dfas)
        mod = ModCounter(dfas, MOD)
        for length in (10_000, 50_000):
            # closed form: 2 ** (length - 1) for length >= 1
            self.assertEqual(exact.count_length(length), 2 ** (length - 1))
            self.assertEqual(mod.count_length(length),
                             pow(2, length - 1, MOD))
        self.assertEqual(
            mod.count_interval(1, 50_000),
            exact.count_interval(1, 50_000) % MOD,
        )

    def test_modular_cannot_select_or_rank(self):
        mod = ModCounter([random_dfa(random.Random(2), 3)], MOD)
        with self.assertRaises(UnsupportedModeError):
            mod.kth_string(3, 0)
        with self.assertRaises(UnsupportedModeError):
            mod.kth_in_interval(0, 3, 0)
        with self.assertRaises(UnsupportedModeError):
            mod.rank("ab")
        with self.assertRaises(UnsupportedModeError):
            mod.enumerator(3)


class PowerBlockTests(unittest.TestCase):
    def test_huge_length_on_unary_automaton(self):
        unary = DFA("a", 0, {0}, {(0, "a"): 1, (1, "a"): 0})
        counter = ExactCounter([unary])
        huge = 2 ** 60
        self.assertEqual(counter.count_length(huge), 1)
        self.assertEqual(counter.count_length(huge + 1), 0)
        self.assertEqual(counter.count_interval(0, huge), huge // 2 + 1)
        self.assertEqual(counter.count_interval(1, huge), huge // 2)
        # Power blocks are reused for a second, differently-shaped query.
        self.assertEqual(counter.count_length(huge + 2), 1)

    def test_blocks_reused_after_version_invalidation(self):
        dfa = DFA("a", 0, {0}, {(0, "a"): 1, (1, "a"): 0})
        counter = ExactCounter([dfa])
        self.assertEqual(counter.count_length(10), 1)
        dfa.set_accepting(1, True)  # now every length is accepted
        self.assertEqual(counter.count_length(10), 1)
        self.assertEqual(counter.count_length(11), 1)


class InfinitudeTests(unittest.TestCase):
    def test_infinite_language_has_verifiable_witness(self):
        even_a = DFA("ab", 0, {0}, {
            (0, "a"): 1, (1, "a"): 0, (0, "b"): 0, (1, "b"): 1,
        })
        no_bb = DFA("ab", 0, {0, 1}, {(0, "a"): 0, (0, "b"): 1, (1, "a"): 0})
        counter = ExactCounter([even_a, no_bb])
        self.assertTrue(counter.is_infinite())
        witness = counter.infinite_witness()
        self.assertIsNotNone(witness)
        self.assertTrue(witness.cycle)
        # Independent verification on the raw DFAs, plus pumped samples.
        self.assertTrue(witness.verify([even_a, no_bb]))
        for repeats in range(6):
            sample = witness.sample(repeats)
            self.assertTrue(accepts_all([even_a, no_bb], sample))
        lengths = {len(witness.sample(i)) for i in range(4)}
        self.assertEqual(len(lengths), 4)

    def test_witness_verifies_on_independently_rebuilt_dfas(self):
        dfa = random_dfa(random.Random(31), 3)
        counter = ExactCounter([dfa])
        witness = counter.infinite_witness()
        if witness is None:
            self.assertFalse(counter.is_infinite())
            return
        rebuilt = DFA(dfa.alphabet, dfa.start, dfa.accepting, dfa.transitions)
        self.assertTrue(witness.verify([rebuilt]))

    def test_reachable_cycle_without_path_to_accept_is_finite(self):
        # a -> rejecting self-loop dead region (reachable, irrelevant cycle);
        # only "b" is accepted, with no continuation.
        dfa = DFA(
            "ab", "s", {"z"},
            {("s", "a"): "c", ("c", "a"): "c", ("s", "b"): "z"},
        )
        counter = ExactCounter([dfa])
        self.assertFalse(counter.is_infinite())
        self.assertIsNone(counter.infinite_witness())
        self.assertEqual(counter.count_length(0), 0)
        self.assertEqual(counter.count_length(1), 1)
        for length in (2, 3, 10):
            self.assertEqual(counter.count_length(length), 0)
        self.assertEqual(counter.kth_string(1, 0), "b")

    def test_empty_and_rejecting_languages_are_finite(self):
        no_accept = DFA("ab", 0, set(), {
            (0, "a"): 0, (0, "b"): 0,
        })
        counter = ExactCounter([no_accept])
        self.assertFalse(counter.is_infinite())
        self.assertEqual(counter.count_length(50), 0)
        with self.assertRaises(IndexError):
            counter.kth_string(50, 0)
        self.assertIsNone(counter.rank("a"))
        enumerator = counter.enumerator(5, budget=2)
        self.assertEqual(list(enumerator), [])
        self.assertTrue(enumerator.done)
        self.assertIsNone(enumerator.checkpoint())

    def test_cycle_must_loop_in_every_component(self):
        # First DFA: single accepting self-loop (infinite alone).
        dfa1 = DFA("ab", 0, {0}, {(0, "a"): 0, (0, "b"): 0})
        # Second DFA: accepts only the empty string.
        dfa2 = DFA("ab", 0, {0}, {
            (0, "a"): 1, (0, "b"): 1, (1, "a"): 1, (1, "b"): 1,
        })
        counter = ExactCounter([dfa1, dfa2])
        self.assertFalse(counter.is_infinite())
        self.assertEqual(counter.count_length(0), 1)
        self.assertEqual(counter.count_interval(0, 100), 1)


class VersionAndCheckpointTests(unittest.TestCase):
    def test_cache_invalidates_after_mutations(self):
        dfa = DFA("ab", 0, {1}, {(0, "a"): 1})
        counter = ExactCounter([dfa])
        self.assertEqual(counter.count_length(1), 1)
        dfa.add_transition(0, "b", 1)
        self.assertEqual(counter.count_length(1), 2)
        dfa.set_accepting(1, False)
        self.assertEqual(counter.count_length(1), 0)
        dfa.set_accepting(1, True)
        dfa.remove_transition(0, "b")
        self.assertEqual(counter.count_length(1), 1)
        # A no-op accepting toggle must not bump the version.
        version = dfa.version
        dfa.set_accepting(1, True)
        self.assertEqual(dfa.version, version)

    def test_checkpoint_resumes_and_matches_full_list(self):
        rng = random.Random(55)
        dfas = [random_dfa(rng, 4), random_dfa(rng, 3)]
        accepted = brute_force(dfas, 8)[6]
        counter = ExactCounter(dfas)
        collected = []
        token = None
        produced_offsets = []
        while True:
            enumerator = counter.enumerator(6, budget=5, checkpoint=token)
            before = enumerator.produced
            batch = list(enumerator)
            produced_offsets.append((before, enumerator.produced))
            collected.extend(batch)
            if enumerator.done:
                break
            self.assertIsNotNone(enumerator.checkpoint())
            token = Checkpoint.loads(enumerator.checkpoint().dumps())
        self.assertEqual(collected, accepted)
        self.assertTrue(all(end - start == 5 or end == len(accepted)
                            for start, end in produced_offsets))

    def test_zero_budget_never_reports_completion(self):
        dfa = DFA("ab", 0, {0}, {(0, "a"): 0, (0, "b"): 0})
        counter = ExactCounter([dfa])
        enumerator = counter.enumerator(2, budget=0)
        self.assertEqual(list(enumerator), [])
        self.assertFalse(enumerator.done)
        token = enumerator.checkpoint()
        self.assertIsNotNone(token)
        self.assertEqual(token.next_index, 0)
        resumed = counter.enumerator(2, budget=10,
                                     checkpoint=Checkpoint.loads(token.dumps()))
        self.assertEqual(len(list(resumed)), 4)
        self.assertTrue(resumed.done)

    def test_checkpoint_rejects_version_change(self):
        dfa = DFA("ab", 0, {0}, {(0, "a"): 0, (0, "b"): 0})
        counter = ExactCounter([dfa])
        enumerator = counter.enumerator(2, budget=1)
        list(enumerator)
        token = enumerator.checkpoint()
        dfa.add_transition(0, "b", 1)
        with self.assertRaises(CheckpointError):
            counter.enumerator(2, budget=1, checkpoint=token)
        # A checkpoint forged for another alphabet is structurally rejected.
        forged = Checkpoint(alphabet=("x", "y"), versions=counter.versions,
                            length=2, next_index=1)
        with self.assertRaises(CheckpointError):
            counter.enumerator(2, checkpoint=forged)

    def test_checkpoint_rejects_length_change(self):
        dfa = DFA("ab", 0, {0}, {(0, "a"): 0, (0, "b"): 0})
        counter = ExactCounter([dfa])
        token = counter.enumerator(2, budget=1).checkpoint()
        with self.assertRaises(CheckpointError):
            counter.enumerator(3, checkpoint=token)


class ConstructionTests(unittest.TestCase):
    def test_alphabet_validation(self):
        with self.assertRaises(ValueError):
            DFA(["ab"], 0)
        with self.assertRaises(ValueError):
            DFA(["a", "a"], 0)
        with self.assertRaises(ValueError):
            DFA([], 0)
        dfa = DFA("ab", 0)
        with self.assertRaises(ValueError):
            dfa.add_transition(0, "c", 1)

    def test_shared_alphabet_required(self):
        dfa1 = DFA("ab", 0)
        dfa2 = DFA("ac", 0)
        with self.assertRaises(ValueError):
            ExactCounter([dfa1, dfa2])

    def test_modulus_validation(self):
        with self.assertRaises(ValueError):
            ModCounter([DFA("a", 0, {0}, {(0, "a"): 0})], 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
