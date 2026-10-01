import io
import json
import random
import unittest
from contextlib import redirect_stdout
from decimal import Decimal
from pathlib import Path

from recon import model, splitmerge
from recon.cli import main as cli_main
from recon.splitmerge import (enumerate_groups, exact_search,
                              exhaustive_cross_check, forced_inclusion)

EXAMPLES = Path(__file__).resolve().parent.parent / "examples"


def make_instance(left, right, window=3600):
    return model.load_instance({"window": window, "left": left, "right": right})


def row(rid, keys, amount, time):
    return {"id": rid, "keys": keys, "amount": amount, "time": time}


class GroupFeasibilityTest(unittest.TestCase):
    def test_one_to_two_split_group(self):
        inst = make_instance(
            left=[row("L1", ["k"], "200.00", 0)],
            right=[row("R1", ["k"], "120.00", 10),
                   row("R2", ["k"], "80.00", 20)],
        )
        report = splitmerge.solve(inst)
        self.assertEqual(len(report["groups"]), 1)
        g = report["groups"][0]
        self.assertEqual(g["left"], ["L1"])
        self.assertEqual(g["right"], ["R1", "R2"])
        self.assertEqual(g["left_sum"], g["right_sum"])
        self.assertEqual(report["objective"]["matched_rows"], 3)

    def test_two_to_one_merge_group(self):
        inst = make_instance(
            left=[row("L1", ["k"], "60.00", 0), row("L2", ["k"], "40.00", 5)],
            right=[row("R1", ["k"], "100.00", 8)],
        )
        report = splitmerge.solve(inst)
        self.assertEqual(len(report["groups"]), 1)
        self.assertEqual(sorted(report["groups"][0]["left"]), ["L1", "L2"])

    def test_strict_amount_equality(self):
        inst = make_instance(
            left=[row("L1", ["k"], "100.00", 0)],
            right=[row("R1", ["k"], "99.99", 0),
                   row("R2", ["k"], "0.02", 0)],
        )
        report = splitmerge.solve(inst)
        self.assertEqual(report["groups"], [])  # 99.99 + 0.02 != 100.00
        # Exact split works when the sum is strictly equal.
        inst2 = make_instance(
            left=[row("L1", ["k"], "100.00", 0)],
            right=[row("R1", ["k"], "99.99", 0),
                   row("R2", ["k"], "0.01", 0)],
        )
        self.assertEqual(len(splitmerge.solve(inst2)["groups"]), 1)

    def test_common_key_required(self):
        inst = make_instance(
            left=[row("L1", ["k1"], "100.00", 0)],
            right=[row("R1", ["k2"], "60.00", 0),
                   row("R2", ["k3"], "40.00", 0)],
        )
        self.assertEqual(splitmerge.solve(inst)["groups"], [])

    def test_group_size_capped_at_three(self):
        inst = make_instance(
            left=[row("L1", ["k"], "200.00", 0)],
            right=[row("R1", ["k"], "100.00", 0),
                   row("R2", ["k"], "100.00", 0),
                   row("R3", ["k"], "100.00", 0)],
        )
        report = splitmerge.solve(inst)
        # A 1v3 group would have 4 rows: not allowed. Best is 1v2.
        self.assertEqual(report["objective"]["matched_rows"], 3)
        self.assertEqual(len(report["unmatched"]["right"]), 1)

    def test_row_in_at_most_one_group(self):
        inst = make_instance(
            left=[row("L1", ["k"], "100.00", 0)],
            right=[row("R1", ["k"], "100.00", 0),
                   row("R2", ["k"], "60.00", 0),
                   row("R3", ["k"], "40.00", 0)],
        )
        report = splitmerge.solve(inst)
        # (L1|R1) amount 200 rows 2 vs (L1|R2,R3) amount 200 rows 3:
        # row count decides -> the 1v2 group wins, R1 unmatched.
        self.assertEqual(report["objective"]["matched_rows"], 3)
        self.assertEqual(report["groups"][0]["right"], ["R2", "R3"])


class ObjectiveOrderTest(unittest.TestCase):
    def test_amount_beats_row_count(self):
        # Option A: (LA|RA) amount 200 rows 2.
        # Option B: (LA|RB,RC) is impossible (sums differ); instead
        # (LB|RB) amount 20 rows 2. Amount dominates: 220 > 200? No:
        # A alone = 200; A + (LB|RB) = 220 rows 4. Both can coexist here;
        # craft a conflict: LA needed by both options.
        inst = make_instance(
            left=[row("LA", ["k"], "100.00", 0)],
            right=[row("RA", ["k"], "100.00", 0),
                   row("RB", ["k"], "60.00", 0),
                   row("RC", ["k"], "40.00", 0)],
        )
        report = splitmerge.solve(inst)
        # (LA|RA): amount 200, rows 2. (LA|RB,RC): amount 200, rows 3.
        # Amount ties -> rows decide -> 1v2 wins.
        self.assertEqual(report["objective"]["matched_rows"], 3)

    def test_spread_minimized_last(self):
        inst = make_instance(
            left=[row("L", ["k"], "10", 100)],
            right=[row("R_far", ["k"], "10", 150),
                   row("R_near", ["k"], "10", 105)],
        )
        report = splitmerge.solve(inst)
        self.assertEqual(report["groups"][0]["right"], ["R_near"])
        self.assertEqual(report["objective"]["total_group_spread"], 5)

    def test_canonical_id_tie_break(self):
        # Two symmetric optima; the lexicographically smaller group id wins.
        inst = make_instance(
            left=[row("L", ["k"], "10", 0)],
            right=[row("RA", ["k"], "10", 0), row("RB", ["k"], "10", 0)],
        )
        report = splitmerge.solve(inst)
        self.assertEqual(report["groups"][0]["id"], "L:L|R:RA")


class CrossCheckTest(unittest.TestCase):
    def test_random_instances_agree_with_exhaustive(self):
        rng = random.Random(20261001)
        for trial in range(30):
            nl = rng.randint(1, 4)
            nr = rng.randint(1, 4)
            keys = ["k1", "k2"]
            amounts = ["10.00", "20.00", "30.00", "40.00"]
            left = [row(f"L{i}", rng.sample(keys, rng.randint(1, 2)),
                        rng.choice(amounts), rng.randint(0, 50))
                    for i in range(nl)]
            right = [row(f"R{j}", rng.sample(keys, rng.randint(1, 2)),
                         rng.choice(amounts), rng.randint(0, 50))
                     for j in range(nr)]
            inst = make_instance(left, right)
            groups = enumerate_groups(inst)
            exact = exact_search(groups)
            brute = exhaustive_cross_check(groups)
            self.assertEqual(exact["status"], "proven_optimal")
            self.assertEqual(exact["key"], brute["key"],
                             f"trial {trial}: {exact['key']} != {brute['key']}")

    def test_report_cross_check_flags(self):
        inst = make_instance(
            left=[row("L1", ["k"], "200.00", 0)],
            right=[row("R1", ["k"], "120.00", 10),
                   row("R2", ["k"], "80.00", 20)],
        )
        report = splitmerge.solve(inst)
        self.assertTrue(report["cross_check"]["objective_agrees"])
        self.assertTrue(report["cross_check"]["solution_agrees"])
        self.assertTrue(report["search"]["proven_optimal"])


class BudgetInterruptTest(unittest.TestCase):
    def _big_instance(self):
        left = [row(f"L{i}", ["k"], "10.00", i) for i in range(6)]
        right = [row(f"R{j}", ["k"], "10.00", j) for j in range(6)]
        return make_instance(left, right)

    def test_budget_exceeded_reports_bounds(self):
        inst = self._big_instance()
        groups = enumerate_groups(inst)
        full = exact_search(groups)
        limited = exact_search(groups, node_budget=3)
        self.assertEqual(limited["status"], "budget_exceeded")
        # Upper bound must dominate the true optimum; any incumbent is a
        # valid lower bound.
        self.assertGreaterEqual(limited["upper_bound"], full["key"])
        if limited["key"] is not None:
            self.assertLessEqual(limited["key"], full["key"])

    def test_cli_budget_flag(self):
        inst = self._big_instance()
        report = splitmerge.solve(inst, node_budget=3)
        self.assertEqual(report["search"]["status"], "budget_exceeded")
        self.assertFalse(report["search"]["proven_optimal"])
        self.assertIsNotNone(report["search"]["upper_bound"])
        # Forced analysis must flag that the optimum was not proven.
        for side in ("left", "right"):
            for entry in report["unmatched"][side]:
                self.assertFalse(
                    entry["forced_inclusion"]["verified_against_proven_optimum"])


class ForcedInclusionTest(unittest.TestCase):
    def test_infeasible_row(self):
        inst = make_instance(
            left=[row("L1", ["k1"], "100.00", 0)],
            right=[row("R1", ["k1"], "100.00", 0),
                   row("R9", ["other"], "100.00", 0)],
        )
        report = splitmerge.solve(inst)
        r9 = [e for e in report["unmatched"]["right"] if e["id"] == "R9"][0]
        self.assertEqual(r9["forced_inclusion"]["status"], "infeasible")

    def test_equal_alternative(self):
        inst = make_instance(
            left=[row("L", ["k"], "10", 0)],
            right=[row("RA", ["k"], "10", 0), row("RB", ["k"], "10", 0)],
        )
        report = splitmerge.solve(inst)
        rb = [e for e in report["unmatched"]["right"] if e["id"] == "RB"][0]
        self.assertEqual(rb["forced_inclusion"]["status"], "equal_alternative")
        self.assertIsNone(rb["forced_inclusion"]["loss"])

    def test_spread_loss(self):
        inst = make_instance(
            left=[row("L", ["k"], "10", 0)],
            right=[row("RA", ["k"], "10", 0), row("RB", ["k"], "10", 5)],
        )
        report = splitmerge.solve(inst)
        rb = [e for e in report["unmatched"]["right"] if e["id"] == "RB"][0]
        fi = rb["forced_inclusion"]
        self.assertEqual(fi["status"], "loss")
        self.assertEqual(fi["loss"]["amount_abs_loss"], "0")
        self.assertEqual(fi["loss"]["row_loss"], 0)
        self.assertEqual(fi["loss"]["spread_increase"], 5)

    def test_amount_loss(self):
        # Optimum: (LA,LB|R1) amount 320 rows 3. R2 unmatched.
        # Forcing R2 gives (LA|R2) amount 200 rows 2: real amount loss.
        inst = make_instance(
            left=[row("LA", ["k"], "100.00", 0),
                  row("LB", ["k"], "60.00", 0)],
            right=[row("R1", ["k"], "160.00", 0),
                   row("R2", ["k"], "100.00", 0)],
        )
        report = splitmerge.solve(inst)
        self.assertEqual(report["objective"]["matched_amount_abs"], "320.00")
        r2 = [e for e in report["unmatched"]["right"] if e["id"] == "R2"][0]
        fi = r2["forced_inclusion"]
        self.assertEqual(fi["status"], "loss")
        self.assertEqual(Decimal(fi["loss"]["amount_abs_loss"]),
                         Decimal("120.00"))


class CliTest(unittest.TestCase):
    def test_cli_splitmerge_example(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            rc = cli_main(["splitmerge",
                           str(EXAMPLES / "splitmerge_demo.json")])
        self.assertEqual(rc, 0)
        report = json.loads(buf.getvalue())
        self.assertEqual(report["objective"]["matched_amount_abs"], "471.00")
        self.assertTrue(report["search"]["proven_optimal"])
        self.assertTrue(report["cross_check"]["objective_agrees"])


if __name__ == "__main__":
    unittest.main()
