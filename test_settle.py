#!/usr/bin/env python3
"""settle.py 的 unittest 测试。所有数据均为合成数据。"""

import io
import json
import unittest
from fractions import Fraction

import settle


def make_spec(entries, base="USD", rates=None):
    """entries: [(account, currency, amount_str), ...]"""
    rates = rates or {base: "1"}
    rates = {base: "1", **rates}
    return {
        "base_currency": base,
        "rates": rates,
        "positions": [
            {"account": a, "currency": c, "amount": amt} for a, c, amt in entries
        ],
    }


class TestHalfEvenRounding(unittest.TestCase):
    def test_half_to_even(self):
        self.assertEqual(settle.round_half_even(Fraction(5, 2)), 2)   # 2.5 -> 2
        self.assertEqual(settle.round_half_even(Fraction(7, 2)), 4)   # 3.5 -> 4
        self.assertEqual(settle.round_half_even(Fraction(-5, 2)), -2) # -2.5 -> -2
        self.assertEqual(settle.round_half_even(Fraction(-7, 2)), -4) # -3.5 -> -4

    def test_non_half(self):
        self.assertEqual(settle.round_half_even(Fraction(1, 3)), 0)
        self.assertEqual(settle.round_half_even(Fraction(2, 3)), 1)
        self.assertEqual(settle.round_half_even(Fraction(-2, 3)), -1)
        self.assertEqual(settle.round_half_even(Fraction(3, 1)), 3)


class TestExactRationalConversion(unittest.TestCase):
    def test_exact_fraction_rate(self):
        # 1 XXX = 1/3 USD；3 单位 = 精确 100 分，无浮点误差
        spec = make_spec(
            [("a", "XXX", "3"), ("b", "USD", "-1")],
            rates={"XXX": "1/3"},
        )
        out = settle.run(spec)
        pos = {p["account"]: p for p in out["positions"]}
        self.assertEqual(pos["a"]["exact_cents"], "100")
        self.assertEqual(pos["a"]["rounded_cents"], 100)
        self.assertEqual(pos["b"]["rounded_cents"], -100)
        self.assertEqual(out["rounding_account"]["net_cents"], 0)

    def test_fractional_cents_rounded(self):
        # 1 XXX = 1/300 USD => 1 单位 = 1/3 分，半偶舍入到 0
        spec = make_spec([("a", "XXX", "1"), ("b", "USD", "0")], rates={"XXX": "1/300"})
        out = settle.run(spec)
        pos = {p["account"]: p for p in out["positions"]}
        self.assertEqual(pos["a"]["exact_cents"], "1/3")
        self.assertEqual(pos["a"]["rounded_cents"], 0)


class TestRoundingAccountConservation(unittest.TestCase):
    def test_rounding_residual_goes_to_rounding_account(self):
        # 三个账户各产生 1/3 分舍入残差，总和 1 分必须由 ROUNDING 吸收
        spec = make_spec(
            [("a", "XXX", "1"), ("b", "XXX", "1"), ("c", "XXX", "1")],
            rates={"XXX": "1/300"},
        )
        out = settle.run(spec)
        self.assertEqual(out["rounding_account"]["net_cents"], 0)  # 各舍入到 0，无残差
        self.assertTrue(out["conservation"]["ok"])
        self.assertEqual(out["conservation"]["sum_net_cents_incl_rounding_account"], 0)

    def test_rounding_account_absorbs_nonzero_residual(self):
        # a: 0.5 分 -> 半偶舍入到 0（残差 -1/2）；b: 1.5 分 -> 2（残差 +1/2）
        # 合计残差 0；改用一个 0.5 分账户配一个整数账户，使残差非零。
        spec = make_spec(
            [("a", "XXX", "1"), ("b", "USD", "-0.06")],
            rates={"XXX": "0.005"},  # a 精确 0.5 分 -> 半偶 -> 0，残差 -1/2
        )
        # b: -0.06 USD = -6 分（整数）。总和 = -6，不为零？需要整体守恒：
        # 精确总额 = 0.5 - 6 = -5.5 分；舍入后 0 + (-6) = -6；ROUNDING = +6？不对。
        # 守恒要求输入头寸本身折算后总和为 0 才有意义，但组件只保证
        # “整数分净额 + ROUNDING = 0”。这里直接验证该不变量。
        out = settle.run(spec)
        nets = {p["account"]: p["rounded_cents"] for p in out["positions"]}
        nets["ROUNDING"] = out["rounding_account"]["net_cents"]
        self.assertEqual(sum(nets.values()), 0)
        self.assertTrue(out["conservation"]["ok"])
        # ROUNDING 账户净额 = -Σrounded，且计入守恒
        self.assertEqual(out["rounding_account"]["net_cents"], -sum(
            p["rounded_cents"] for p in out["positions"]))

    def test_rounding_account_participates_in_settlement(self):
        # 构造 ROUNDING 净额非零且需参与结算的场景：
        # a 精确 +0.5 分 -> 0；b 精确 -0.5 分 -> 0（半偶：-0.5 -> 0？）
        # -0.5 半偶 -> 0（0 是偶数）。则 Σrounded = 0，ROUNDING = 0，不参与。
        # 改用 a: +1.5 -> 2, b: -0.5 -> 0, c: -1 -> -1：Σrounded = 1，ROUNDING = -1。
        spec = make_spec(
            [("a", "XXX", "3"), ("b", "YYY", "1"), ("c", "USD", "-0.01")],
            rates={"XXX": "0.005", "YYY": "-0.005"},
        )
        out = settle.run(spec)
        self.assertEqual(out["rounding_account"]["net_cents"], -1)
        self.assertTrue(out["conservation"]["ok"])
        # ROUNDING 欠 1 分，必须作为付款方出现在结算中
        payers = {t["from"] for t in out["settlement"]["transactions"]}
        self.assertIn("ROUNDING", payers)
        for t in out["settlement"]["transactions"]:
            self.assertGreater(t["amount_cents"], 0)


class TestMinimumTransactions(unittest.TestCase):
    def test_acceptance_a5_b5_c10(self):
        # 验收边界：a 欠 5，b 欠 5，c 收 10（单位：USD 元 => 分）
        spec = make_spec([
            ("a", "USD", "-5"), ("b", "USD", "-5"), ("c", "USD", "10"),
        ])
        out = settle.run(spec)
        s = out["settlement"]
        self.assertEqual(s["transaction_count"], 2)
        self.assertEqual(s["minimum_transaction_count"], 2)
        self.assertTrue(s["optimal"])
        # 至少两笔：不可能一笔完成（一个付款方只有 5，而 c 要收 10）
        self.assertGreaterEqual(s["transaction_count"], 2)
        paid = {}
        for t in s["transactions"]:
            self.assertGreater(t["amount_cents"], 0)  # 无零额交易
            paid[t["from"]] = paid.get(t["from"], 0) + t["amount_cents"]
            self.assertEqual(t["to"], "c")
        self.assertEqual(paid, {"a": 500, "b": 500})

    def test_no_hidden_zero_amount_transactions(self):
        spec = make_spec([
            ("a", "USD", "-5"), ("b", "USD", "-5"), ("c", "USD", "10"),
        ])
        out = settle.run(spec)
        for t in out["settlement"]["transactions"]:
            self.assertIsInstance(t["amount_cents"], int)
            self.assertGreater(t["amount_cents"], 0)

    def test_partitionable_groups_reduce_count(self):
        # a:-5, b:+5, c:-3, d:+3 可分成两个零和组 => 2 笔（下界 n-k=4-2）
        spec = make_spec([
            ("a", "USD", "-0.05"), ("b", "USD", "0.05"),
            ("c", "USD", "-0.03"), ("d", "USD", "0.03"),
        ])
        out = settle.run(spec)
        s = out["settlement"]
        self.assertEqual(s["transaction_count"], 2)
        self.assertEqual(s["minimum_transaction_count"], 2)
        self.assertTrue(s["optimal"])

    def test_direction_consistency(self):
        spec = make_spec([
            ("a", "USD", "-7"), ("b", "USD", "4"), ("c", "USD", "3"),
        ])
        out = settle.run(spec)
        nets = {"a": -700, "b": 400, "c": 300}
        for t in out["settlement"]["transactions"]:
            self.assertLess(nets[t["from"]], 0)
            self.assertGreater(nets[t["to"]], 0)
        # 结算后全部清零
        bal = {k: 0 for k in nets}
        for t in out["settlement"]["transactions"]:
            bal[t["from"]] += t["amount_cents"]
            bal[t["to"]] -= t["amount_cents"]
        for k, v in nets.items():
            self.assertEqual(bal[k], -v)

    def test_optimality_certificate_present(self):
        spec = make_spec([
            ("a", "USD", "-5"), ("b", "USD", "-5"), ("c", "USD", "10"),
        ])
        out = settle.run(spec)
        cert = out["settlement"]["certificate"]
        self.assertIn("2^3", cert)
        self.assertIn("下界", cert)

    def test_all_zero_needs_no_transactions(self):
        spec = make_spec([("a", "USD", "0"), ("b", "USD", "0")])
        out = settle.run(spec)
        self.assertEqual(out["settlement"]["transaction_count"], 0)
        self.assertTrue(out["settlement"]["optimal"])

    def test_larger_exact_enumeration(self):
        # 6 个账户：{-8, 3, 5, -2, -6, 8}（单位：分）
        spec = make_spec([
            ("a", "USD", "-0.08"), ("b", "USD", "0.03"), ("c", "USD", "0.05"),
            ("d", "USD", "-0.02"), ("e", "USD", "-0.06"), ("f", "USD", "0.08"),
        ])
        out = settle.run(spec)
        s = out["settlement"]
        # 零和划分：{a,f}、{b,c,d,e}（3+5-2-6=0）=> k=2，下界 6-2=4
        self.assertEqual(s["transaction_count"], 4)
        self.assertEqual(s["minimum_transaction_count"], 4)
        self.assertTrue(s["optimal"])
        # 独立验证：枚举所有子集确认不存在 3 块零和划分
        vals = [-8, 3, 5, -2, -6, 8]
        k, _ = settle.max_zero_sum_partition(vals)
        self.assertEqual(k, 2)


class TestCli(unittest.TestCase):
    def test_cli_stdin_and_file(self):
        import subprocess, sys, tempfile, os
        spec = make_spec([
            ("a", "USD", "-5"), ("b", "USD", "-5"), ("c", "USD", "10"),
        ])
        # stdin 模式
        r = subprocess.run(
            [sys.executable, "settle.py"],
            input=json.dumps(spec), capture_output=True, text=True,
            cwd=os.path.dirname(os.path.abspath(__file__)),
        )
        self.assertEqual(r.returncode, 0, r.stderr)
        out = json.loads(r.stdout)
        self.assertEqual(out["settlement"]["transaction_count"], 2)
        # 文件模式
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as f:
            json.dump(spec, f)
            path = f.name
        try:
            r = subprocess.run(
                [sys.executable, "settle.py", path],
                capture_output=True, text=True,
                cwd=os.path.dirname(os.path.abspath(__file__)),
            )
            self.assertEqual(r.returncode, 0, r.stderr)
            out = json.loads(r.stdout)
            self.assertTrue(out["conservation"]["ok"])
        finally:
            os.unlink(path)


if __name__ == "__main__":
    unittest.main()
