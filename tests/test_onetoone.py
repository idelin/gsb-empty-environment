import unittest
from decimal import Decimal

from recon.common import Row
from recon.onetoone import solve_onetoone

D = Decimal


def row(rid, keys, amount, time):
    return Row(id=rid, keys=frozenset(keys), amount=D(amount), time=D(time))


class AcceptanceBoundaryTest(unittest.TestCase):
    """Left a candidates {x, y}; left b only {x}.  Maximum matching must be
    a-y and b-x; a greedy choice of x for a would lose b."""

    def setUp(self):
        self.left = [row("a", ["k1", "k2"], "10", 0),
                     row("b", ["k1"], "10", 0)]
        self.right = [row("x", ["k1"], "10", 0),
                      row("y", ["k2"], "10", 0)]

    def test_maximum_matching_is_ay_bx(self):
        report = solve_onetoone(self.left, self.right, D(0))
        pairs = {(p["left"], p["right"]) for p in report["pairs"]}
        self.assertEqual(pairs, {("a", "y"), ("b", "x")})
        self.assertEqual(report["objectives"]["matched_pairs"], 2)
        self.assertTrue(report["verification"]["optimal"])

    def test_greedy_would_lose_b(self):
        # Simulate the naive greedy: rows in input order, first compatible
        # right row (sorted by id) wins.
        used = set()
        greedy = []
        for l in self.left:
            for r in sorted(self.right, key=lambda r: r.id):
                if r.id in used:
                    continue
                if l.keys & r.keys and l.amount == r.amount:
                    greedy.append((l.id, r.id))
                    used.add(r.id)
                    break
        self.assertEqual(greedy, [("a", "x")])  # b left unmatched
        report = solve_onetoone(self.left, self.right, D(0))
        self.assertGreater(report["objectives"]["matched_pairs"], len(greedy))


class ObjectiveOrderTest(unittest.TestCase):
    def test_total_time_diff_minimized_after_cardinality(self):
        left = [row("l1", ["k"], "10", 0), row("l2", ["k"], "10", 1)]
        right = [row("r1", ["k"], "10", 0), row("r2", ["k"], "10", 4)]
        report = solve_onetoone(left, right, D(4))
        pairs = {(p["left"], p["right"]) for p in report["pairs"]}
        # {l1-r1 (0), l2-r2 (3)} sums to 3; {l1-r2 (4), l2-r1 (1)} sums to 5
        self.assertEqual(pairs, {("l1", "r1"), ("l2", "r2")})
        self.assertEqual(report["objectives"]["total_time_diff"], "3")

    def test_pair_id_lexicographic_tiebreak(self):
        left = [row("l1", ["k"], "10", 0), row("l2", ["k"], "10", 0)]
        right = [row("r1", ["k"], "10", 1), row("r2", ["k"], "10", 5)]
        # Both perfect matchings have total time diff 6; the one containing
        # pair id "l1::r1" is lexicographically smaller.
        report = solve_onetoone(left, right, D(5))
        pairs = {(p["left"], p["right"]) for p in report["pairs"]}
        self.assertEqual(pairs, {("l1", "r1"), ("l2", "r2")})
        self.assertTrue(report["verification"]["optimal"])


class ConstraintTest(unittest.TestCase):
    def test_amount_must_match_exactly(self):
        left = [row("a", ["k"], "10.00", 0)]
        right = [row("x", ["k"], "10.01", 0)]
        report = solve_onetoone(left, right, D(100))
        self.assertEqual(report["objectives"]["matched_pairs"], 0)

    def test_window_is_enforced(self):
        left = [row("a", ["k"], "10", 0)]
        right = [row("x", ["k"], "10", 301)]
        report = solve_onetoone(left, right, D(300))
        self.assertEqual(report["objectives"]["matched_pairs"], 0)
        report2 = solve_onetoone(left, right, D(301))
        self.assertEqual(report2["objectives"]["matched_pairs"], 1)

    def test_shared_key_required(self):
        left = [row("a", ["k1"], "10", 0)]
        right = [row("x", ["k2"], "10", 0)]
        report = solve_onetoone(left, right, D(0))
        self.assertEqual(report["objectives"]["matched_pairs"], 0)

    def test_row_used_at_most_once(self):
        left = [row("a", ["k"], "10", 0), row("b", ["k"], "10", 0)]
        right = [row("x", ["k"], "10", 0)]
        report = solve_onetoone(left, right, D(0))
        self.assertEqual(report["objectives"]["matched_pairs"], 1)


class UnmatchedExplanationTest(unittest.TestCase):
    def test_no_candidate_vs_competition(self):
        left = [row("a", ["k1"], "10", 0),
                row("b", ["k1"], "10", 0),
                row("c", ["zzz"], "99", 0)]
        right = [row("x", ["k1"], "10", 0)]
        report = solve_onetoone(left, right, D(0))
        reasons = {u["id"]: u["reason"] for u in report["unmatched"]}
        self.assertEqual(reasons["c"], "no_candidate")
        self.assertEqual(reasons["b"], "competition")
        comp = next(u for u in report["unmatched"] if u["id"] == "b")
        self.assertTrue(comp["evidence"]["all_compatible_right_matched"])
        self.assertIn("x", comp["evidence"]["alternating_reachable_right"])

    def test_verification_certificate(self):
        left = [row("a", ["k1", "k2"], "10", 0), row("b", ["k1"], "10", 0)]
        right = [row("x", ["k1"], "10", 0), row("y", ["k2"], "10", 0)]
        report = solve_onetoone(left, right, D(0))
        v = report["verification"]
        self.assertEqual(v["matching_size"], v["vertex_cover_size"])
        self.assertTrue(v["augmenting_path_free"])
        self.assertFalse(v["residual_negative_cycle_found"])
        self.assertTrue(v["optimal"])


if __name__ == "__main__":
    unittest.main()
