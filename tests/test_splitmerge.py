import random
import unittest
from decimal import Decimal

from recon.common import Row
from recon.splitmerge import (enumerate_groups, exact_search,
                              solve_splitmerge)

D = Decimal


def row(rid, keys, amount, time):
    return Row(id=rid, keys=frozenset(keys), amount=D(amount), time=D(time))


class GroupFeasibilityTest(unittest.TestCase):
    def test_one_to_two_split_group(self):
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "6", 0), row("R2", ["k"], "4", 0)]
        report = solve_splitmerge(left, right, D(0), verify_exhaustive=True)
        self.assertEqual(len(report["groups"]), 1)
        g = report["groups"][0]
        self.assertEqual(g["gid"], "L1=R1+R2")
        self.assertEqual(report["objective"]["matched_abs_amount"], "20")
        self.assertEqual(report["objective"]["matched_rows"], 3)
        self.assertTrue(report["optimal"])

    def test_two_to_one_merge_group(self):
        left = [row("L1", ["k"], "3", 0), row("L2", ["k"], "7", 0)]
        right = [row("R1", ["k"], "10", 0)]
        report = solve_splitmerge(left, right, D(0))
        self.assertEqual([g["gid"] for g in report["groups"]], ["L1+L2=R1"])

    def test_common_key_required_for_group(self):
        left = [row("L1", ["k1"], "10", 0)]
        right = [row("R1", ["k2"], "6", 0), row("R2", ["k3"], "4", 0)]
        report = solve_splitmerge(left, right, D(0))
        self.assertEqual(report["groups"], [])

    def test_amount_sum_must_be_strictly_equal(self):
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "6", 0), row("R2", ["k"], "5", 0)]
        report = solve_splitmerge(left, right, D(0))
        self.assertEqual(report["groups"], [])

    def test_group_span_must_fit_window(self):
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "6", 0), row("R2", ["k"], "4", 301)]
        report = solve_splitmerge(left, right, D(300))
        self.assertEqual(report["groups"], [])
        report2 = solve_splitmerge(left, right, D(301))
        self.assertEqual(len(report2["groups"]), 1)


class ObjectiveOrderTest(unittest.TestCase):
    def test_amount_beats_row_count(self):
        # pair L1=R1 matches |amount| 20 with 2 rows; group L1=R2+R3 matches
        # 19 with 3 rows.  Amount wins.
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 0), row("R2", ["k"], "5", 0),
                 row("R3", ["k"], "4", 0)]
        report = solve_splitmerge(left, right, D(0))
        self.assertEqual([g["gid"] for g in report["groups"]], ["L1=R1"])
        self.assertEqual(report["objective"]["matched_rows"], 2)

    def test_row_count_breaks_amount_tie(self):
        # pair L1=R1 and group L1=R2+R3 both match |amount| 20; the group
        # matches 3 rows instead of 2.
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 0), row("R2", ["k"], "6", 0),
                 row("R3", ["k"], "4", 0)]
        report = solve_splitmerge(left, right, D(0))
        self.assertEqual([g["gid"] for g in report["groups"]], ["L1=R2+R3"])
        self.assertEqual(report["objective"]["matched_rows"], 3)

    def test_span_breaks_row_count_tie(self):
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 1), row("R2", ["k"], "10", 5)]
        report = solve_splitmerge(left, right, D(10))
        self.assertEqual([g["gid"] for g in report["groups"]], ["L1=R1"])
        self.assertEqual(report["objective"]["total_span"], "1")

    def test_canonical_gid_breaks_span_tie(self):
        left = [row("L1", ["k"], "10", 0), row("L2", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 0)]
        # L1=R1 and L2=R1 tie on every numeric objective; "L1=R1" wins.
        report = solve_splitmerge(left, right, D(0))
        self.assertEqual([g["gid"] for g in report["groups"]], ["L1=R1"])

    def test_rows_used_in_at_most_one_group(self):
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 0), row("R2", ["k"], "6", 0),
                 row("R3", ["k"], "4", 0)]
        report = solve_splitmerge(left, right, D(0))
        used = []
        for g in report["groups"]:
            used.extend(g["left"] + g["right"])
        self.assertEqual(len(used), len(set(used)))


class ForcedInclusionTest(unittest.TestCase):
    def test_infeasible_row(self):
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "9", 0)]
        report = solve_splitmerge(left, right, D(0))
        fi = {u["id"]: u["forced_inclusion"] for u in report["unmatched"]}
        self.assertEqual(fi["L1"]["status"], "infeasible")
        self.assertEqual(fi["R1"]["status"], "infeasible")

    def test_equal_alternative(self):
        left = [row("L1", ["k"], "10", 0), row("L2", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 0)]
        report = solve_splitmerge(left, right, D(0))
        fi = {u["id"]: u["forced_inclusion"] for u in report["unmatched"]}
        self.assertEqual(fi["L2"]["status"], "equal_alternative")

    def test_real_loss(self):
        # L1 can pair with R1 (amount 20) or be forced out by the bigger
        # group L1+L2=R1? amounts: L1+L2=11 != 10, so instead: forcing R2
        # (which only fits group L1=R2+R3) displaces pair L1=R1.
        left = [row("L1", ["k"], "10", 0)]
        right = [row("R1", ["k"], "10", 0), row("R2", ["k"], "5", 0),
                 row("R3", ["k"], "5", 0)]
        report = solve_splitmerge(left, right, D(0))
        # Optimum: group L1=R2+R3 (amount 20, 3 rows) beats pair (20, 2).
        self.assertEqual([g["gid"] for g in report["groups"]], ["L1=R2+R3"])
        fi = {u["id"]: u["forced_inclusion"] for u in report["unmatched"]}
        self.assertEqual(fi["R1"]["status"], "loss")
        self.assertEqual(fi["R1"]["loss"]["rows"], 1)


class BudgetAndVerificationTest(unittest.TestCase):
    def test_budget_interruption_reports_bounds(self):
        left = [row(f"L{i}", ["k"], "10", 0) for i in range(4)]
        right = [row(f"R{i}", ["k"], "10", 0) for i in range(4)]
        report = solve_splitmerge(left, right, D(0), budget=0)
        self.assertTrue(report["search"]["interrupted"])
        self.assertFalse(report["optimal"])
        self.assertIn("upper_bound", report["search"])
        self.assertIn("lower_bound", report["search"])

    def test_exhaustive_crosscheck_matches(self):
        left = [row("L1", ["k"], "10", 0), row("L2", ["k"], "4", 1)]
        right = [row("R1", ["k"], "6", 0), row("R2", ["k"], "4", 1),
                 row("R3", ["k"], "10", 2)]
        report = solve_splitmerge(left, right, D(5), verify_exhaustive=True)
        chk = report["search"]["exhaustive_check"]
        self.assertTrue(chk["performed"])
        self.assertTrue(chk["objectives_match"])
        self.assertTrue(report["optimal"])

    def test_random_instances_agree_with_exhaustive(self):
        rng = random.Random(20261001)
        for trial in range(30):
            nL = rng.randint(1, 4)
            nR = rng.randint(1, 4)
            left = [row(f"L{i}", [f"k{rng.randint(0, 2)}"],
                        str(rng.randint(1, 6)), rng.randint(0, 5))
                    for i in range(nL)]
            right = [row(f"R{j}", [f"k{rng.randint(0, 2)}"],
                         str(rng.randint(1, 6)), rng.randint(0, 5))
                     for j in range(nR)]
            rows = left + right
            groups = enumerate_groups(left, right, D(3))
            pruned = exact_search(rows, groups)
            brute = exact_search(rows, groups, prune=False)
            self.assertEqual(pruned["objective"], brute["objective"],
                             f"trial {trial}")
            self.assertFalse(pruned["interrupted"])


if __name__ == "__main__":
    unittest.main()
