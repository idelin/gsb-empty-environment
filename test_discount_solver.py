"""discount_solver 的 unittest 测试. 所有数据均为合成数据."""

import json
import random
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from discount_solver import (InputError, Offer, Item, brute_force,
                             compare_with_brute_force, parse_cart, solve)

MODULE = Path(__file__).with_name("discount_solver.py")


def item(item_id, price, quantity=1):
    return Item(id=item_id, price=price, quantity=quantity)


def offer(offer_id, amount, item_ids, min_subtotal=0, mutex_group=None):
    return Offer(id=offer_id, amount=amount, item_ids=tuple(item_ids),
                 min_subtotal=min_subtotal, mutex_group=mutex_group)


class AcceptanceBoundaryTest(unittest.TestCase):
    """题目给定的两条验收边界."""

    def test_mutex_picks_larger_discount(self):
        # 单商品原价10, 互斥优惠减6与减7, 应只选减7
        items = [item("A", 10)]
        offers = [offer("OFF6", 6, ["A"], mutex_group="g"),
                  offer("OFF7", 7, ["A"], mutex_group="g")]
        result = solve(items, offers)
        self.assertEqual(result["final_payment"], 3)
        self.assertEqual(result["total_discount"], 7)
        self.assertEqual(result["sequence"], ["OFF7"])
        self.assertEqual(result["steps"][0]["allocation"], {"A": 7})
        self.assertEqual(result["steps"][0]["remaining_after"], {"A": 3})
        excluded = {e["offer_id"]: e["reason"] for e in result["excluded"]}
        self.assertEqual(excluded, {"OFF6": "mutex_conflict"})

    def test_non_mutex_stacked_discount_never_negative(self):
        # 两个非互斥各减6, 最终实付0而不是-2
        items = [item("A", 10)]
        offers = [offer("O1", 6, ["A"]), offer("O2", 6, ["A"])]
        result = solve(items, offers)
        self.assertEqual(result["final_payment"], 0)
        self.assertEqual(result["total_discount"], 10)  # 6 + 4, 不是 12
        self.assertEqual(result["sequence"], ["O1", "O2"])
        self.assertEqual(result["steps"][0]["effective_discount"], 6)
        self.assertEqual(result["steps"][1]["effective_discount"], 4)
        self.assertEqual(result["excluded"], [])


class SemanticsTest(unittest.TestCase):
    def test_threshold_uses_original_price(self):
        # 门槛按原价: A减8后剩余2, B的门槛10仍按原价满足, 可继续减
        items = [item("A", 10)]
        offers = [offer("BIG", 8, ["A"], min_subtotal=10),
                  offer("SMALL", 5, ["A"], min_subtotal=10)]
        result = solve(items, offers)
        self.assertEqual(result["final_payment"], 0)
        self.assertEqual(result["sequence"], ["BIG", "SMALL"])
        self.assertEqual(result["steps"][1]["effective_discount"], 2)

    def test_threshold_not_met_excluded(self):
        items = [item("A", 5)]
        offers = [offer("X", 3, ["A"], min_subtotal=10)]
        result = solve(items, offers)
        self.assertEqual(result["final_payment"], 5)
        self.assertEqual(result["sequence"], [])
        self.assertEqual(result["excluded"][0]["reason"], "threshold_not_met")

    def test_order_matters(self):
        # X=10, Y=10; A减15覆盖{X,Y}, B减8覆盖{X}
        # 先B后A: 8 + min(15, 2+10) = 20; 先A后B: 15 + 0 = 15
        items = [item("X", 10), item("Y", 10)]
        offers = [offer("A", 15, ["X", "Y"]), offer("B", 8, ["X"])]
        result = solve(items, offers)
        self.assertEqual(result["final_payment"], 0)
        self.assertEqual(result["total_discount"], 20)
        self.assertEqual(result["sequence"], ["B", "A"])
        self.assertEqual(result["steps"][0]["allocation"], {"X": 8})
        self.assertEqual(result["steps"][1]["allocation"], {"X": 2, "Y": 10})

    def test_lexicographic_tie_break(self):
        # 两个优惠等效, 实付相同, 选ID序列字典序最小者
        items = [item("A", 5)]
        offers = [offer("Z9", 5, ["A"]), offer("A1", 5, ["A"])]
        result = solve(items, offers)
        self.assertEqual(result["final_payment"], 0)
        self.assertEqual(result["sequence"], ["A1"])
        excluded = {e["offer_id"]: e["reason"] for e in result["excluded"]}
        self.assertEqual(excluded, {"Z9": "no_remaining_payable"})

    def test_subset_coverage_only(self):
        items = [item("A", 10), item("B", 100)]
        offers = [offer("ONLY_A", 50, ["A"])]
        result = solve(items, offers)
        # 折扣不超过覆盖商品剩余应付: 只减A的10
        self.assertEqual(result["total_discount"], 10)
        self.assertEqual(result["final_payment"], 100)
        self.assertEqual(result["steps"][0]["allocation"], {"A": 10})

    def test_quantity_multiplies_price(self):
        items = [item("A", 4, quantity=3)]  # 小计12
        offers = [offer("O", 10, ["A"])]
        result = solve(items, offers)
        self.assertEqual(result["original_total"], 12)
        self.assertEqual(result["final_payment"], 2)

    def test_no_offers(self):
        result = solve([item("A", 7)], [])
        self.assertEqual(result["final_payment"], 7)
        self.assertEqual(result["sequence"], [])
        self.assertEqual(result["steps"], [])
        self.assertEqual(result["excluded"], [])

    def test_each_offer_at_most_once(self):
        items = [item("A", 100)]
        offers = [offer("O", 30, ["A"])]
        result = solve(items, offers)
        self.assertEqual(result["sequence"], ["O"])
        self.assertEqual(result["final_payment"], 70)

    def test_mutex_group_of_three_picks_best(self):
        items = [item("A", 20)]
        offers = [offer("M1", 5, ["A"], mutex_group="g"),
                  offer("M2", 9, ["A"], mutex_group="g"),
                  offer("M3", 7, ["A"], mutex_group="g")]
        result = solve(items, offers)
        self.assertEqual(result["sequence"], ["M2"])
        self.assertEqual(result["final_payment"], 11)
        reasons = {e["offer_id"]: e["reason"] for e in result["excluded"]}
        self.assertEqual(reasons, {"M1": "mutex_conflict",
                                   "M3": "mutex_conflict"})


class InputValidationTest(unittest.TestCase):
    def test_duplicate_offer_id_rejected(self):
        with self.assertRaises(InputError):
            parse_cart({"items": [{"id": "A", "price": 1}],
                        "offers": [{"id": "O", "amount": 1},
                                   {"id": "O", "amount": 2}]})

    def test_unknown_item_rejected(self):
        with self.assertRaises(InputError):
            parse_cart({"items": [{"id": "A", "price": 1}],
                        "offers": [{"id": "O", "amount": 1,
                                    "item_ids": ["B"]}]})

    def test_non_positive_amount_rejected(self):
        with self.assertRaises(InputError):
            parse_cart({"items": [{"id": "A", "price": 1}],
                        "offers": [{"id": "O", "amount": 0}]})

    def test_default_item_ids_covers_all(self):
        items, offers = parse_cart({
            "items": [{"id": "A", "price": 3}, {"id": "B", "price": 4}],
            "offers": [{"id": "O", "amount": 5}]})
        self.assertEqual(offers[0].item_ids, ("A", "B"))


class BruteForceCrossCheckTest(unittest.TestCase):
    """主求解器与小规模穷举对照器交叉验证(合成随机数据)."""

    def test_handpicked_cases(self):
        cases = [
            ([item("A", 10)],
             [offer("O1", 6, ["A"]), offer("O2", 6, ["A"])]),
            ([item("X", 10), item("Y", 10)],
             [offer("A", 15, ["X", "Y"]), offer("B", 8, ["X"])]),
            ([item("A", 10), item("B", 6)],
             [offer("O1", 9, ["A", "B"], min_subtotal=12),
              offer("O2", 4, ["A"], mutex_group="g"),
              offer("O3", 5, ["A", "B"], mutex_group="g"),
              offer("O4", 3, ["B"], min_subtotal=8)]),  # O4 门槛不满足
        ]
        for items, offers in cases:
            report = compare_with_brute_force(items, offers)
            self.assertIs(report["match"], True, msg=str(report))

    def test_randomized_cross_check(self):
        rng = random.Random(20260927)
        for case_no in range(300):
            n_items = rng.randint(1, 3)
            items = [item(f"I{k}", rng.randint(1, 20),
                          rng.randint(1, 2)) for k in range(n_items)]
            ids = [it.id for it in items]
            offers = []
            for j in range(rng.randint(0, 6)):
                covered = rng.sample(ids, rng.randint(1, len(ids)))
                group = rng.choice([None, None, "g1", "g2"])
                offers.append(offer(f"O{j}", rng.randint(1, 15), covered,
                                    min_subtotal=rng.randint(0, 25),
                                    mutex_group=group))
            report = compare_with_brute_force(items, offers)
            self.assertIs(report["match"], True,
                          msg=f"case {case_no}: {report}")

    def test_brute_force_refuses_large_input(self):
        items = [item("A", 100)]
        offers = [offer(f"O{i}", 1, ["A"]) for i in range(9)]
        self.assertIsNone(brute_force(items, offers, max_offers=8))


class CliTest(unittest.TestCase):
    def _run_cli(self, args, stdin_text=None):
        return subprocess.run(
            [sys.executable, str(MODULE), *args],
            input=stdin_text, capture_output=True, text=True)

    def test_cli_file_input_with_verify(self):
        payload = {"items": [{"id": "A", "price": 10}],
                   "offers": [
                       {"id": "OFF6", "amount": 6, "mutex_group": "g"},
                       {"id": "OFF7", "amount": 7, "mutex_group": "g"}]}
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False, encoding="utf-8") as fh:
            json.dump(payload, fh)
            path = fh.name
        try:
            proc = self._run_cli(["--verify", path])
        finally:
            Path(path).unlink()
        self.assertEqual(proc.returncode, 0, msg=proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["final_payment"], 3)
        self.assertEqual(out["sequence"], ["OFF7"])
        self.assertIs(out["brute_force_check"]["match"], True)

    def test_cli_stdin(self):
        payload = {"items": [{"id": "A", "price": 10}],
                   "offers": [{"id": "O1", "amount": 6},
                              {"id": "O2", "amount": 6}]}
        proc = self._run_cli([], stdin_text=json.dumps(payload))
        self.assertEqual(proc.returncode, 0, msg=proc.stderr)
        out = json.loads(proc.stdout)
        self.assertEqual(out["final_payment"], 0)
        self.assertEqual(out["total_discount"], 10)

    def test_cli_invalid_input_exit_code(self):
        proc = self._run_cli([], stdin_text='{"items": []}')
        self.assertEqual(proc.returncode, 2)
        self.assertIn("error", proc.stderr)


if __name__ == "__main__":
    unittest.main()
