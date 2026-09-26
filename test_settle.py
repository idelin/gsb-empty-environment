import unittest
from fractions import Fraction

import settle


def spec_from_nets(nets):
    """构造单币种、单笔债权形式的输入，使各账户精确净额等于给定值（分）。"""
    claims = []
    for name, cents in nets.items():
        if cents < 0:
            claims.append({"from": name, "to": "sink", "amount": str(Fraction(-cents, 100)), "currency": "USD"})
        elif cents > 0:
            claims.append({"from": "sink", "to": name, "amount": str(Fraction(cents, 100)), "currency": "USD"})
    return {"base": "USD", "rates": {"USD": "1"}, "claims": claims}


class TestRationalAndRounding(unittest.TestCase):
    def test_parse_rational(self):
        self.assertEqual(settle.parse_rational("6/5"), Fraction(6, 5))
        self.assertEqual(settle.parse_rational("10.5"), Fraction(21, 2))
        self.assertEqual(settle.parse_rational(3), Fraction(3))
        with self.assertRaises(ValueError):
            settle.parse_rational(0.1)  # 浮点不允许

    def test_half_even(self):
        # 0.5 分 -> 0（偶），1.5 分 -> 2（偶），2.5 分 -> 2（偶）
        self.assertEqual(settle.round_half_even_cents(Fraction(1, 200)), 0)
        self.assertEqual(settle.round_half_even_cents(Fraction(3, 200)), 2)
        self.assertEqual(settle.round_half_even_cents(Fraction(5, 200)), 2)
        self.assertEqual(settle.round_half_even_cents(Fraction(-1, 200)), 0)
        self.assertEqual(settle.round_half_even_cents(Fraction(-3, 200)), -2)
        self.assertEqual(settle.round_half_even_cents(Fraction(1, 400)), 0)
        self.assertEqual(settle.round_half_even_cents(Fraction(3, 400)), 1)

    def test_exact_rational_rate(self):
        # 1/3 的精确汇率：3 笔各 1 单位 -> 精确合计 1.00，不得有浮点漂移
        spec = {
            "base": "USD",
            "rates": {"USD": "1", "X": "1/3"},
            "claims": [
                {"from": "a", "to": "b", "amount": "1", "currency": "X"},
                {"from": "a", "to": "b", "amount": "1", "currency": "X"},
                {"from": "a", "to": "b", "amount": "1", "currency": "X"},
            ],
        }
        nets = settle.compute_exact_nets(spec)
        self.assertEqual(nets["a"], Fraction(-1))
        self.assertEqual(nets["b"], Fraction(1))
        result = settle.run(spec)
        self.assertEqual(result["rounding_account"]["net_cents"], 0)
        self.assertEqual(result["rounded_nets_cents"], {"a": -100, "b": 100})


class TestRoundingAccount(unittest.TestCase):
    def test_rounding_residual_booked_and_conserved(self):
        # 三方各 1/3 分残差：a、b 各应收 1/3 分 -> 舍入为 0；c 应付 2/3 分 -> 舍入为 -1
        spec = {
            "base": "USD",
            "rates": {"USD": "1"},
            "claims": [
                {"from": "c", "to": "a", "amount": "0.003333333333333333333333333333", "currency": "USD"},
                {"from": "c", "to": "b", "amount": "0.003333333333333333333333333333", "currency": "USD"},
            ],
        }
        result = settle.run(spec)
        rounded = result["rounded_nets_cents"]
        self.assertEqual(rounded["a"], 0)
        self.assertEqual(rounded["b"], 0)
        self.assertEqual(rounded["c"], -1)
        # 差额 +1 分必须显式记入舍入账户
        self.assertEqual(result["rounding_account"]["net_cents"], 1)
        self.assertEqual(rounded[settle.ROUNDING_ACCOUNT], 1)
        # 含舍入账户的总额守恒
        self.assertTrue(result["conservation_ok"])
        self.assertEqual(sum(rounded.values()), 0)
        self.assertEqual(result["verification_errors"], [])


class TestMinTransactions(unittest.TestCase):
    def test_two_debtors_one_creditor_needs_two(self):
        # 验收边界：a 欠 5，b 欠 5，c 收 10 -> 至少两笔
        nets = {"a": -5, "b": -5, "c": 10}
        plan = settle.build_settlement(nets)
        self.assertEqual(len(plan["transactions"]), 2)
        self.assertEqual(plan["optimality"]["lower_bound"], 2)
        self.assertTrue(plan["optimality"]["optimal"])
        self.assertEqual(settle.verify_settlement(nets, plan), [])

    def test_no_hidden_zero_amount_transactions(self):
        nets = {"a": -5, "b": -5, "c": 10}
        plan = settle.build_settlement(nets)
        for tx in plan["transactions"]:
            self.assertIsInstance(tx["amount"], int)
            self.assertGreater(tx["amount"], 0)
            self.assertLess(nets[tx["from"]], 0)
            self.assertGreater(nets[tx["to"]], 0)

    def test_three_pairs_three_transactions(self):
        nets = {"a": -7, "b": -3, "c": -2, "x": 7, "y": 3, "z": 2}
        plan = settle.build_settlement(nets)
        self.assertEqual(len(plan["transactions"]), 3)
        self.assertEqual(plan["optimality"]["zero_sum_groups"], 3)
        self.assertTrue(plan["optimality"]["optimal"])
        self.assertEqual(settle.verify_settlement(nets, plan), [])

    def test_single_group_chain(self):
        # 无法拆分的零和组：4 个账户需要 3 笔
        nets = {"a": -10, "b": 4, "c": 5, "d": 1}
        plan = settle.build_settlement(nets)
        self.assertEqual(len(plan["transactions"]), 3)
        self.assertEqual(plan["optimality"]["zero_sum_groups"], 1)
        self.assertTrue(plan["optimality"]["optimal"])
        self.assertEqual(settle.verify_settlement(nets, plan), [])

    def test_zero_net_accounts_excluded(self):
        nets = {"a": -5, "b": 5, "idle": 0}
        plan = settle.build_settlement(nets)
        self.assertEqual(len(plan["transactions"]), 1)
        self.assertEqual(plan["optimality"]["active_accounts"], 2)

    def test_direction_consistency(self):
        nets = {"a": -8, "b": -2, "c": 6, "d": 4}
        plan = settle.build_settlement(nets)
        for tx in plan["transactions"]:
            self.assertLess(nets[tx["from"]], 0)
            self.assertGreater(nets[tx["to"]], 0)
        self.assertEqual(settle.verify_settlement(nets, plan), [])

    def test_unbalanced_nets_rejected(self):
        with self.assertRaises(ValueError):
            settle.build_settlement({"a": -5, "b": 4})


class TestEndToEnd(unittest.TestCase):
    def test_multi_currency(self):
        spec = {
            "base": "USD",
            "rates": {"USD": "1", "EUR": "6/5", "JPY": "1/150"},
            "claims": [
                {"from": "alice", "to": "bob", "amount": "10", "currency": "EUR"},
                {"from": "bob", "to": "carol", "amount": "1500", "currency": "JPY"},
                {"from": "carol", "to": "alice", "amount": "2", "currency": "USD"},
            ],
        }
        result = settle.run(spec)
        # alice: -12 + 2 = -10; bob: +12 - 10 = +2; carol: +10 - 2 = +8
        self.assertEqual(
            result["rounded_nets_cents"],
            {"alice": -1000, "bob": 200, "carol": 800},
        )
        self.assertEqual(result["rounding_account"]["net_cents"], 0)
        self.assertEqual(len(result["settlement"]["transactions"]), 2)
        self.assertTrue(result["settlement"]["optimality"]["optimal"])
        self.assertTrue(result["conservation_ok"])
        self.assertEqual(result["verification_errors"], [])


if __name__ == "__main__":
    unittest.main()
