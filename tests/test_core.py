import itertools
import unittest

from dfa_min.core import (
    DEAD_STATE,
    build_dfa,
    complete,
    minimize,
    prepare,
    shortest_distinguishing_strings,
    trim,
)

SAMPLE = {
    "alphabet": ["0", "1"],
    "states": ["A", "B", "C", "D", "E"],
    "start": "A",
    "accepting": ["B", "C"],
    "transitions": {
        "A": {"0": "E", "1": "B"},
        "B": {"0": "B", "1": "B"},
        "C": {"0": "C", "1": "B"},
        "D": {"0": "A", "1": "A"},
        "E": {"1": "C"},
    },
}


def sample_dfa():
    return build_dfa(SAMPLE["alphabet"], SAMPLE["states"], SAMPLE["transitions"],
                     SAMPLE["start"], SAMPLE["accepting"])


def run_word(dfa, state, word):
    s = state
    for ch in word:
        s = dfa.transitions[s][ch]
    return s in dfa.accepting


def brute_force_witness(dfa, p, q, max_len):
    for length in range(max_len + 1):
        for tup in itertools.product(dfa.alphabet, repeat=length):
            w = "".join(tup)
            if run_word(dfa, p, w) != run_word(dfa, q, w):
                return w
    return None


class TestCompletionAndTrim(unittest.TestCase):
    def test_missing_transitions_filled_with_dead_state(self):
        comp = complete(sample_dfa())
        self.assertEqual(comp.states - sample_dfa().states, {DEAD_STATE})
        self.assertEqual(comp.transitions["E"]["0"], DEAD_STATE)
        self.assertEqual(comp.transitions[DEAD_STATE],
                         {"0": DEAD_STATE, "1": DEAD_STATE})
        self.assertNotIn(DEAD_STATE, comp.accepting)

    def test_complete_dfa_unchanged(self):
        comp = complete(sample_dfa())
        self.assertIs(complete(comp), comp)

    def test_trim_removes_unreachable(self):
        prepared = prepare(sample_dfa())
        self.assertNotIn("D", prepared.states)
        self.assertEqual(prepared.states, {"A", "B", "C", "E", DEAD_STATE})

    def test_dead_state_kept_when_reachable(self):
        prepared = prepare(sample_dfa())
        self.assertIn(DEAD_STATE, prepared.states)


class TestMinimization(unittest.TestCase):
    def setUp(self):
        self.cert = minimize(sample_dfa())

    def test_equivalent_states_merge(self):
        self.assertEqual(self.cert.mapping["B"], self.cert.mapping["C"])
        self.assertEqual(len(self.cert.quotient.states), 4)

    def test_inequivalent_states_stay_separate(self):
        m = self.cert.mapping
        self.assertEqual(len({m["A"], m["B"], m["E"], m[DEAD_STATE]}), 4)

    def test_accepting_never_merged_with_nonaccepting(self):
        prepared = self.cert.prepared
        for p in prepared.states:
            for q in prepared.states:
                if self.cert.mapping[p] == self.cert.mapping[q]:
                    self.assertEqual(p in prepared.accepting, q in prepared.accepting)

    def test_unreachable_state_has_no_mapping(self):
        self.assertNotIn("D", self.cert.mapping)
        self.assertEqual(self.cert.removed_unreachable, ("D",))
        self.assertEqual(self.cert.dead_state, DEAD_STATE)

    def test_witnesses_distinguish_and_are_shortest(self):
        prepared = self.cert.prepared
        table = self.cert.state_witnesses
        states = sorted(prepared.states)
        bound = 2 * len(states)
        for i, p in enumerate(states):
            for q in states[i + 1:]:
                brute = brute_force_witness(prepared, p, q, bound)
                key = (p, q)
                if brute is None:
                    self.assertNotIn(key, table)
                else:
                    self.assertIn(key, table)
                    w = table[key]
                    self.assertEqual(len(w), len(brute))
                    self.assertNotEqual(run_word(prepared, p, w),
                                        run_word(prepared, q, w))

    def test_certificate_covers_every_quotient_pair(self):
        quot = self.cert.quotient
        states = sorted(quot.states)
        self.assertEqual(len(self.cert.witnesses),
                         len(states) * (len(states) - 1) // 2)
        for (x, y), w in self.cert.witnesses.items():
            self.assertNotEqual(run_word(quot, x, w), run_word(quot, y, w))

    def test_quotient_pair_witnesses_are_shortest(self):
        quot = self.cert.quotient
        states = sorted(quot.states)
        bound = 2 * len(states)
        for i, x in enumerate(states):
            for y in states[i + 1:]:
                key = (x, y) if (x, y) in self.cert.witnesses else (y, x)
                brute = brute_force_witness(quot, x, y, bound)
                self.assertIsNotNone(brute)
                self.assertEqual(len(self.cert.witnesses[key]), len(brute))


class TestRenamingInvariance(unittest.TestCase):
    def test_renamed_input_gives_same_canonical_quotient(self):
        cert1 = minimize(sample_dfa())
        rename = {s: f"state_{i}" for i, s in enumerate(sorted(SAMPLE["states"]))}
        renamed = build_dfa(
            SAMPLE["alphabet"],
            [rename[s] for s in SAMPLE["states"]],
            {rename[s]: {a: rename[t] for a, t in row.items()}
             for s, row in SAMPLE["transitions"].items()},
            rename[SAMPLE["start"]],
            [rename[s] for s in SAMPLE["accepting"]],
        )
        cert2 = minimize(renamed)
        self.assertEqual(cert1.quotient, cert2.quotient)
        self.assertEqual(cert1.witnesses, cert2.witnesses)


class TestEdgeCases(unittest.TestCase):
    def test_empty_alphabet_nonaccepting(self):
        dfa = build_dfa([], ["S"], {}, "S", [])
        cert = minimize(dfa)
        self.assertEqual(len(cert.quotient.states), 1)
        self.assertEqual(cert.quotient.accepting, frozenset())
        self.assertEqual(cert.witnesses, {})
        self.assertEqual(cert.mapping, {"S": "q0"})

    def test_empty_alphabet_accepting(self):
        dfa = build_dfa([], ["S"], {}, "S", ["S"])
        cert = minimize(dfa)
        self.assertEqual(cert.quotient.accepting, frozenset({"q0"}))

    def test_empty_language_collapses_to_single_state(self):
        dfa = build_dfa(["a", "b"], ["S", "T"],
                        {"S": {"a": "T", "b": "S"}, "T": {"a": "T", "b": "S"}},
                        "S", [])
        cert = minimize(dfa)
        self.assertEqual(len(cert.quotient.states), 1)
        self.assertEqual(cert.quotient.accepting, frozenset())

    def test_validation_rejects_bad_input(self):
        with self.assertRaises(ValueError):
            build_dfa(["a"], ["S"], {"S": {"b": "S"}}, "S", [])
        with self.assertRaises(ValueError):
            build_dfa(["a"], ["S"], {"S": {"a": "X"}}, "S", [])
        with self.assertRaises(ValueError):
            build_dfa(["a"], ["S"], {}, "X", [])


if __name__ == "__main__":
    unittest.main()
