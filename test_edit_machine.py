import unittest

from edit_machine import EditDistanceNFA, distance


ALPHABET = "abcdefghijklmnopqrstuvwxyz"


def dp_distance(a, b, ins=1, dele=1, sub=1):
    row = list(range(len(b) + 1))
    for i, ca in enumerate(a, 1):
        nxt = [i * dele]
        for j, cb in enumerate(b, 1):
            nxt.append(
                min(
                    nxt[-1] + ins,
                    row[j] + dele,
                    row[j - 1] + (0 if ca == cb else sub),
                )
            )
        row = nxt
    return row[-1]


class EditDistanceTests(unittest.TestCase):
    def test_classic_levenshtein(self):
        self.assertEqual(distance("kitten", "sitting", ALPHABET, 3), 3)
        result = EditDistanceNFA.build("kitten", ALPHABET, 3).stream()
        result.feed_all("sitting")
        best = result.best()
        self.assertEqual(best.distance, 3)
        ops = [e.op for e in best.edits]
        self.assertEqual(ops, ["substitute", "match", "match", "match",
                               "substitute", "match", "insert"])

    def test_matches_dynamic_programming(self):
        cases = [("abc", "abc"), ("abc", "axc"), ("kitten", "sitting"),
                 ("flaw", "lawn"), ("", "abc"), ("abc", ""), ("aa", "aba")]
        for p, t in cases:
            expected = dp_distance(p, t)
            self.assertEqual(distance(p, t, ALPHABET, 5), expected, (p, t))

    def test_empty_pattern_length_bound(self):
        machine = EditDistanceNFA.build("", ALPHABET, 2)
        self.assertIsNone(distance("", "abc", ALPHABET, 2))
        self.assertEqual(distance("", "ab", ALPHABET, 2), 2)
        self.assertEqual(distance("", "", ALPHABET, 2), 0)

    def test_transposition_off_by_default(self):
        self.assertIsNone(distance("ab", "ba", ALPHABET, 1))
        self.assertEqual(distance("ab", "ba", ALPHABET, 2), 2)

    def test_transposition_when_declared(self):
        result = (
            EditDistanceNFA.build("abcd", ALPHABET, 1, transposition_cost=1)
            .stream()
            .feed_all("acbd")
            .result()
        )
        self.assertTrue(result.accepted)
        self.assertEqual(result.distance, 1)
        self.assertEqual([e.op for e in result.edits],
                         ["match", "transpose", "match"])

    def test_explicit_costs(self):
        machine = EditDistanceNFA.build(
            "cat", ALPHABET, 2, deletion_cost=2
        )
        result = machine.stream().feed_all("ct").result()
        self.assertTrue(result.accepted)
        self.assertEqual(result.distance, 2)
        machine = EditDistanceNFA.build(
            "cat", ALPHABET, 1, deletion_cost=2
        )
        self.assertFalse(machine.stream().feed_all("ct").result().accepted)

    def test_streaming_keeps_optimal_cost_per_state(self):
        machine = EditDistanceNFA.build("aa", ALPHABET, 2)
        stream = machine.stream()
        stream.feed("a")
        self.assertEqual(stream.state_costs[("n", 1)], 0)
        stream.feed("x")
        stream.feed("a")
        costs = stream.state_costs
        for state, cost in costs.items():
            self.assertEqual(cost, dp_distance("aa"[: state[1]], "axa"))
        self.assertEqual(stream.best().distance, 1)

    def test_incremental_matches_one_shot(self):
        machine = EditDistanceNFA.build("abc", ALPHABET, 2)
        incremental = machine.stream()
        for ch in "axc":
            incremental.feed(ch)
        one_shot = machine.stream().feed_all("axc")
        self.assertEqual(incremental.state_costs, one_shot.state_costs)

    def test_path_reconstruction_roundtrip(self):
        result = (
            EditDistanceNFA.build("abc", ALPHABET, 3)
            .stream()
            .feed_all("xabyc")
            .best()
        )
        rebuilt_text = "".join(ch for e in result.edits for ch in e.text)
        rebuilt_pattern = "".join(ch for e in result.edits for ch in e.pattern)
        self.assertEqual(rebuilt_text, "xabyc")
        self.assertEqual(rebuilt_pattern, "abc")
        self.assertEqual(sum(e.cost for e in result.edits), result.distance)
        self.assertEqual(result.text_alignment.replace("-", ""), "xabyc")
        self.assertEqual(result.pattern_alignment.replace("-", ""), "abc")

    def test_explicit_machine_construction(self):
        # Language: exactly one symbol; states and transitions supplied by hand.
        states = ("q0", "q1")
        sym = {"q0": {"a": [("q1", 0, None)]}}
        eps = {}
        machine = EditDistanceNFA(states, "q0", {"q1"}, sym, eps, "a", 0)
        self.assertTrue(machine.stream().feed_all("a").result().accepted)
        self.assertFalse(machine.stream().feed_all("").result().accepted)

    def test_alphabet_is_checked(self):
        machine = EditDistanceNFA.build("a", "a", 1)
        with self.assertRaises(ValueError):
            machine.stream().feed("b")
        with self.assertRaises(ValueError):
            EditDistanceNFA.build("ab", "a", 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
