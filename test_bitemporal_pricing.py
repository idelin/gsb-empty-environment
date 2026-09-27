"""bitemporal_pricing 的 unittest 测试。所有数据均为合成数据。"""
from __future__ import annotations

import json
import subprocess
import sys
import unittest
from pathlib import Path

from bitemporal_pricing import BitemporalPriceLedger, PriceRule

MODULE = Path(__file__).with_name("bitemporal_pricing.py")


def rule(product="P1", currency="CNY", valid_from=0, valid_to=20,
         amount_cents=1000, revision=1, known_from=2) -> PriceRule:
    return PriceRule(product, currency, valid_from, valid_to,
                     amount_cents, revision, known_from)


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界: 价格10在业务[0,20)有效且知悉时间2;
    价格15在[5,10)有效且知悉时间8。"""

    def setUp(self) -> None:
        self.ledger = BitemporalPriceLedger()
        self.ledger.add_rule(rule(amount_cents=1000, revision=1, known_from=2))
        self.ledger.add_rule(rule(valid_from=5, valid_to=10,
                                  amount_cents=1500, revision=2, known_from=8))

    def test_business7_known7_gives_10(self):
        self.assertEqual(
            self.ledger.query("P1", "CNY", 7, 7).amount_cents, 1000)

    def test_business7_known8_gives_15(self):
        self.assertEqual(
            self.ledger.query("P1", "CNY", 7, 8).amount_cents, 1500)

    def test_business10_gives_10(self):
        # [5,10) 右开, 业务10不在迟到规则范围内
        for known in (8, 9, 100):
            with self.subTest(known=known):
                self.assertEqual(
                    self.ledger.query("P1", "CNY", 10, known).amount_cents, 1000)


class OverlapResolutionTest(unittest.TestCase):
    def test_same_known_time_resolved_by_max_revision(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=1, known_from=3))
        ledger.add_rule(rule(amount_cents=200, revision=2, known_from=3))
        self.assertEqual(ledger.query("P1", "CNY", 1, 3).amount_cents, 200)

    def test_later_known_time_wins_over_revision(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=9, known_from=1))
        ledger.add_rule(rule(amount_cents=200, revision=1, known_from=5))
        self.assertEqual(ledger.query("P1", "CNY", 1, 5).amount_cents, 200)

    def test_left_closed_right_open_interval(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(valid_from=5, valid_to=10, amount_cents=500))
        self.assertIsNone(ledger.query("P1", "CNY", 4, 100))
        self.assertEqual(ledger.query("P1", "CNY", 5, 100).amount_cents, 500)
        self.assertEqual(ledger.query("P1", "CNY", 9, 100).amount_cents, 500)
        self.assertIsNone(ledger.query("P1", "CNY", 10, 100))

    def test_different_product_or_currency_isolated(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(product="P1", amount_cents=100))
        ledger.add_rule(rule(product="P2", amount_cents=200))
        ledger.add_rule(rule(product="P1", currency="USD", amount_cents=300))
        self.assertEqual(ledger.query("P1", "CNY", 1, 100).amount_cents, 100)
        self.assertEqual(ledger.query("P2", "CNY", 1, 100).amount_cents, 200)
        self.assertEqual(ledger.query("P1", "USD", 1, 100).amount_cents, 300)
        self.assertIsNone(ledger.query("P2", "USD", 1, 100))


class RevisionConflictTest(unittest.TestCase):
    def test_same_revision_different_content_rejected(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=1))
        with self.assertRaises(ValueError):
            ledger.add_rule(rule(amount_cents=999, revision=1))
        with self.assertRaises(ValueError):
            ledger.add_rule(rule(valid_to=21, revision=1))

    def test_same_revision_same_content_idempotent(self):
        ledger = BitemporalPriceLedger()
        self.assertTrue(ledger.add_rule(rule(amount_cents=100, revision=1)))
        self.assertFalse(ledger.add_rule(rule(amount_cents=100, revision=1)))
        self.assertEqual(len(ledger._rules), 1)

    def test_invalid_rule_rejected(self):
        ledger = BitemporalPriceLedger()
        with self.assertRaises(ValueError):  # 空区间
            ledger.add_rule(rule(valid_from=10, valid_to=10))
        with self.assertRaises(ValueError):  # 负金额
            ledger.add_rule(rule(amount_cents=-1))
        with self.assertRaises(ValueError):  # 非整数
            ledger.add_rule(rule(amount_cents=10.5))  # type: ignore[arg-type]


class RevocationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.ledger = BitemporalPriceLedger()
        self.ledger.add_rule(rule(amount_cents=1000, revision=1, known_from=2))
        self.ledger.revoke("P1", "CNY", 1, known_from=12)

    def test_revoke_only_affects_queries_after_revoke_known(self):
        self.assertEqual(
            self.ledger.query("P1", "CNY", 1, 11).amount_cents, 1000)
        self.assertIsNone(self.ledger.query("P1", "CNY", 1, 12))
        self.assertIsNone(self.ledger.query("P1", "CNY", 1, 99))

    def test_revoke_unknown_revision_rejected(self):
        with self.assertRaises(KeyError):
            self.ledger.revoke("P1", "CNY", 999, known_from=1)

    def test_revoke_reveals_earlier_rule(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=1, known_from=1))
        ledger.add_rule(rule(amount_cents=200, revision=2, known_from=5))
        ledger.revoke("P1", "CNY", 2, known_from=9)
        self.assertEqual(ledger.query("P1", "CNY", 1, 8).amount_cents, 200)
        self.assertEqual(ledger.query("P1", "CNY", 1, 9).amount_cents, 100)


class EvidenceTest(unittest.TestCase):
    def test_evidence_lists_all_involved_rules(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=1000, revision=1, known_from=2))
        ledger.add_rule(rule(valid_from=5, valid_to=10,
                             amount_cents=1500, revision=2, known_from=8))
        ledger.add_rule(rule(valid_from=100, valid_to=200,
                             amount_cents=1, revision=3, known_from=1))
        result = ledger.explain("P1", "CNY", 7, 8)
        self.assertEqual(result["amount_cents"], 1500)
        self.assertEqual(len(result["evidence"]), 2)  # 区间外的规则不涉及
        selected = [e for e in result["evidence"] if e["selected"]]
        self.assertEqual(len(selected), 1)
        self.assertEqual(selected[0]["rule"]["revision"], 2)
        self.assertEqual(result["winner"]["amount_cents"], 1500)

    def test_evidence_marks_revoked_rules(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=1, known_from=1))
        ledger.add_rule(rule(amount_cents=200, revision=2, known_from=5))
        ledger.revoke("P1", "CNY", 2, known_from=9)
        result = ledger.explain("P1", "CNY", 1, 9)
        by_rev = {e["rule"]["revision"]: e for e in result["evidence"]}
        self.assertTrue(by_rev[2]["revoked_at_known_time"])
        self.assertFalse(by_rev[1]["revoked_at_known_time"])
        self.assertTrue(by_rev[1]["selected"])
        self.assertEqual(result["amount_cents"], 100)

    def test_evidence_respects_known_time_visibility(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=1, known_from=2))
        ledger.add_rule(rule(amount_cents=200, revision=2, known_from=8))
        result = ledger.explain("P1", "CNY", 1, 7)
        self.assertEqual(len(result["evidence"]), 1)  # 知悉8的规则不可见


class CacheInvalidationTest(unittest.TestCase):
    def test_late_revision_invalidates_only_affected_entries(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(valid_from=0, valid_to=100,
                             amount_cents=100, revision=1, known_from=1))
        # 建立三个缓存项: 受影响区间内外 + 知悉时间早于迟到修订
        ledger.query("P1", "CNY", 7, 5)    # 受影响: bt 在 [5,10), kt>=8? 否
        ledger.query("P1", "CNY", 7, 50)   # 受影响: bt 在 [5,10) 且 kt>=8
        ledger.query("P1", "CNY", 50, 50)  # 不受影响: bt 不在 [5,10)
        self.assertEqual(ledger.cache_size(), 3)
        # 迟到修订: 业务[5,10), 知悉8
        ledger.add_rule(rule(valid_from=5, valid_to=10,
                             amount_cents=200, revision=2, known_from=8))
        keys = set(ledger.cache_keys())
        self.assertIn(("P1", "CNY", 7, 5), keys)    # kt<8 不受影响
        self.assertIn(("P1", "CNY", 50, 50), keys)  # 区间外不受影响
        self.assertNotIn(("P1", "CNY", 7, 50), keys)  # 唯一被失效
        self.assertEqual(ledger.cache_size(), 2)
        # 重新查询得到新结果
        self.assertEqual(ledger.query("P1", "CNY", 7, 50).amount_cents, 200)
        self.assertEqual(ledger.query("P1", "CNY", 50, 50).amount_cents, 100)

    def test_revoke_invalidates_only_affected_entries(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(valid_from=0, valid_to=10,
                             amount_cents=100, revision=1, known_from=1))
        ledger.query("P1", "CNY", 5, 5)    # kt<撤销知悉时间, 保留
        ledger.query("P1", "CNY", 5, 50)   # 失效
        ledger.query("P1", "CNY", 20, 50)  # 区间外, 保留
        ledger.revoke("P1", "CNY", 1, known_from=10)
        keys = set(ledger.cache_keys())
        self.assertIn(("P1", "CNY", 5, 5), keys)
        self.assertIn(("P1", "CNY", 20, 50), keys)
        self.assertNotIn(("P1", "CNY", 5, 50), keys)
        self.assertIsNone(ledger.query("P1", "CNY", 5, 50))
        self.assertEqual(ledger.query("P1", "CNY", 5, 5).amount_cents, 100)

    def test_cache_hit_returns_consistent_result(self):
        ledger = BitemporalPriceLedger()
        ledger.add_rule(rule(amount_cents=100, revision=1, known_from=1))
        first = ledger.query("P1", "CNY", 1, 10)
        self.assertIn(("P1", "CNY", 1, 10), set(ledger.cache_keys()))
        second = ledger.query("P1", "CNY", 1, 10)
        self.assertIs(first, second)


class CliTest(unittest.TestCase):
    def run_cli(self, lines: list[dict]) -> list[dict]:
        payload = "\n".join(json.dumps(x) for x in lines) + "\n"
        proc = subprocess.run(
            [sys.executable, str(MODULE)],
            input=payload, capture_output=True, text=True, timeout=30,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        return [json.loads(line) for line in proc.stdout.splitlines()]

    def test_acceptance_scenario_end_to_end(self):
        out = self.run_cli([
            {"op": "add", "product": "P1", "currency": "CNY", "valid_from": 0,
             "valid_to": 20, "amount_cents": 1000, "revision": 1, "known_from": 2},
            {"op": "add", "product": "P1", "currency": "CNY", "valid_from": 5,
             "valid_to": 10, "amount_cents": 1500, "revision": 2, "known_from": 8},
            {"op": "query", "product": "P1", "currency": "CNY",
             "business_time": 7, "known_time": 7},
            {"op": "query", "product": "P1", "currency": "CNY",
             "business_time": 7, "known_time": 8},
            {"op": "query", "product": "P1", "currency": "CNY",
             "business_time": 10, "known_time": 8},
        ])
        self.assertEqual([o["ok"] for o in out], [True] * 5)
        self.assertEqual(out[2]["amount_cents"], 1000)
        self.assertEqual(out[3]["amount_cents"], 1500)
        self.assertEqual(out[4]["amount_cents"], 1000)

    def test_cli_explain_revoke_conflict_and_error_line(self):
        out = self.run_cli([
            {"op": "add", "product": "P1", "currency": "CNY", "valid_from": 0,
             "valid_to": 20, "amount_cents": 1000, "revision": 1, "known_from": 2},
            {"op": "add", "product": "P1", "currency": "CNY", "valid_from": 0,
             "valid_to": 20, "amount_cents": 9999, "revision": 1, "known_from": 3},
            {"op": "revoke", "product": "P1", "currency": "CNY",
             "revision": 1, "known_from": 12},
            {"op": "explain", "product": "P1", "currency": "CNY",
             "business_time": 7, "known_time": 12},
            {"op": "query", "product": "P1", "currency": "CNY",
             "business_time": 7, "known_time": 11},
            {"op": "cache_stats"},
        ])
        self.assertTrue(out[0]["ok"] and out[0]["inserted"])
        self.assertFalse(out[1]["ok"])  # 同修订号不同内容被拒绝
        self.assertIn("修订号冲突", out[1]["error"])
        self.assertTrue(out[2]["ok"])
        self.assertIsNone(out[3]["amount_cents"])  # 知悉12时已被撤销
        self.assertTrue(out[3]["evidence"][0]["revoked_at_known_time"])
        self.assertEqual(out[4]["amount_cents"], 1000)  # 知悉11仍可见
        self.assertTrue(out[5]["ok"])
        self.assertGreaterEqual(out[5]["size"], 1)


if __name__ == "__main__":
    unittest.main()
