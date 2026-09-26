"""bitemporal_ledger 的 unittest 测试。所有数据均为合成数据。"""

import json
import os
import subprocess
import sys
import tempfile
import unittest

from bitemporal_ledger import (
    ConflictError,
    Ledger,
    LedgerError,
    UnknownRevisionError,
)


def make_acceptance_ledger() -> Ledger:
    """验收场景：价格10 业务[0,20) 知悉2；价格15 业务[5,10) 知悉8。"""
    ledger = Ledger()
    ledger.insert("SKU1", "USD", 10, 0, 20, revision=1, known_at=2)
    ledger.insert("SKU1", "USD", 15, 5, 10, revision=2, known_at=8)
    return ledger


class AcceptanceTest(unittest.TestCase):
    def test_boundary_scenario(self):
        ledger = make_acceptance_ledger()
        # 业务7、知悉7 -> 10（迟到修订尚不知悉）
        self.assertEqual(ledger.query("SKU1", "USD", 7, 7).amount, 10)
        # 业务7、知悉8 -> 15（迟到修订已知悉，知悉时间更大者胜出）
        self.assertEqual(ledger.query("SKU1", "USD", 7, 8).amount, 15)
        # 业务10 -> 10（[5,10) 右开，10 不在迟到修订区间内）
        self.assertEqual(ledger.query("SKU1", "USD", 10, 8).amount, 10)
        self.assertEqual(ledger.query("SKU1", "USD", 10, 100).amount, 10)

    def test_interval_left_closed_right_open(self):
        ledger = make_acceptance_ledger()
        self.assertEqual(ledger.query("SKU1", "USD", 5, 8).amount, 15)   # 左闭
        self.assertEqual(ledger.query("SKU1", "USD", 9, 8).amount, 15)
        self.assertEqual(ledger.query("SKU1", "USD", 0, 2).amount, 10)    # 左闭
        self.assertIsNone(ledger.query("SKU1", "USD", 20, 100).amount)    # 右开
        self.assertIsNone(ledger.query("SKU1", "USD", -1, 100).amount)


class OverlapResolutionTest(unittest.TestCase):
    def test_same_known_at_higher_revision_wins(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=1)
        ledger.insert("P", "USD", 200, 0, 10, revision=2, known_at=1)
        self.assertEqual(ledger.query("P", "USD", 5, 1).amount, 200)

    def test_later_known_at_wins_over_revision(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=9, known_at=1)
        ledger.insert("P", "USD", 200, 0, 10, revision=1, known_at=5)
        # 知悉时间优先于修订号
        self.assertEqual(ledger.query("P", "USD", 5, 5).amount, 200)

    def test_different_product_or_currency_isolated(self):
        ledger = make_acceptance_ledger()
        self.assertIsNone(ledger.query("SKU2", "USD", 7, 100).amount)
        self.assertIsNone(ledger.query("SKU1", "EUR", 7, 100).amount)


class RevocationTest(unittest.TestCase):
    def test_revoke_only_affects_later_knowledge(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=1)
        ledger.insert("P", "USD", 200, 0, 10, revision=2, known_at=3)
        ledger.revoke("P", "USD", revision=2, known_at=6)
        # 撤销知悉之前：仍看到 200
        self.assertEqual(ledger.query("P", "USD", 5, 5).amount, 200)
        # 撤销知悉之后（含该时刻）：回退到 100
        self.assertEqual(ledger.query("P", "USD", 5, 6).amount, 100)
        self.assertEqual(ledger.query("P", "USD", 5, 100).amount, 100)

    def test_revoke_all_rules_yields_none(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=1)
        ledger.revoke("P", "USD", revision=1, known_at=2)
        self.assertIsNone(ledger.query("P", "USD", 5, 2).amount)
        self.assertEqual(ledger.query("P", "USD", 5, 1).amount, 100)

    def test_revoke_unknown_revision_rejected(self):
        ledger = Ledger()
        with self.assertRaises(UnknownRevisionError):
            ledger.revoke("P", "USD", revision=99, known_at=1)

    def test_revoke_idempotent(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=1)
        first = ledger.revoke("P", "USD", 1, known_at=4)
        second = ledger.revoke("P", "USD", 1, known_at=9)
        self.assertEqual(first, 4)
        self.assertEqual(second, 4)  # 保留首次撤销知悉时间
        self.assertIsNone(ledger.query("P", "USD", 5, 4).amount)


class ConflictTest(unittest.TestCase):
    def test_same_revision_different_content_rejected(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=1)
        with self.assertRaises(ConflictError):
            ledger.insert("P", "USD", 101, 0, 10, revision=1, known_at=2)
        with self.assertRaises(ConflictError):
            ledger.insert("P", "USD", 100, 0, 11, revision=1, known_at=2)

    def test_same_revision_same_content_idempotent(self):
        ledger = Ledger()
        first = ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=3)
        second = ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=7)
        self.assertEqual(first, 3)
        self.assertEqual(second, 3)  # 幂等：保留原知悉时间
        self.assertEqual(ledger.query("P", "USD", 5, 3).amount, 100)

    def test_amount_must_be_int_cents(self):
        ledger = Ledger()
        for bad in (10.5, "100", True, None):
            with self.assertRaises(LedgerError):
                ledger.insert("P", "USD", bad, 0, 10, revision=1)
        with self.assertRaises(LedgerError):
            ledger.insert("P", "USD", -1, 0, 10, revision=1)

    def test_empty_interval_rejected(self):
        ledger = Ledger()
        with self.assertRaises(LedgerError):
            ledger.insert("P", "USD", 100, 10, 10, revision=1)
        with self.assertRaises(LedgerError):
            ledger.insert("P", "USD", 100, 10, 5, revision=1)


class EvidenceTest(unittest.TestCase):
    def test_evidence_lists_all_involved_rules(self):
        ledger = make_acceptance_ledger()
        ledger.revoke("SKU1", "USD", revision=2, known_at=9)
        result = ledger.query("SKU1", "USD", 7, 9)
        self.assertEqual(result.amount, 10)
        self.assertEqual(len(result.evidence), 2)
        by_rev = {e.rule.revision: e for e in result.evidence}
        self.assertFalse(by_rev[1].revoked)
        self.assertTrue(by_rev[1].in_business_range)
        self.assertTrue(by_rev[2].revoked)          # 知悉9时已撤销
        self.assertTrue(by_rev[2].in_business_range)
        self.assertEqual(result.winner.revision, 1)

    def test_evidence_excludes_unknown_at_query_time(self):
        ledger = make_acceptance_ledger()
        result = ledger.query("SKU1", "USD", 7, 7)
        self.assertEqual([e.rule.revision for e in result.evidence], [1])

    def test_evidence_marks_out_of_range_rules(self):
        ledger = make_acceptance_ledger()
        result = ledger.query("SKU1", "USD", 12, 8)
        by_rev = {e.rule.revision: e for e in result.evidence}
        self.assertTrue(by_rev[1].in_business_range)
        self.assertFalse(by_rev[2].in_business_range)  # [5,10) 不含 12
        self.assertEqual(result.amount, 10)


class CacheInvalidationTest(unittest.TestCase):
    def test_late_revision_invalidates_only_affected(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 20, revision=1, known_at=1)
        # 建立缓存：受影响项与不受影响项
        hit_affected = ledger.query("P", "USD", 7, 5)     # 知悉5 < 迟到知悉8
        ledger.query("P", "USD", 7, 10)                   # 受影响：知悉10 >= 8
        ledger.query("P", "USD", 15, 10)                  # 不受影响：业务15 不在 [5,10)
        ledger.query("Q", "USD", 7, 10)                   # 不受影响：不同商品
        self.assertEqual(ledger.cache_size, 4)

        ledger.insert("P", "USD", 150, 5, 10, revision=2, known_at=8)
        # 只有 (P,USD,7,10) 被失效
        self.assertEqual(ledger.cache_size, 3)
        self.assertIs(ledger.query("P", "USD", 7, 5), hit_affected)  # 缓存仍命中
        self.assertEqual(ledger.query("P", "USD", 7, 10).amount, 150)
        self.assertEqual(ledger.query("P", "USD", 15, 10).amount, 100)

    def test_revoke_invalidates_only_affected(self):
        ledger = Ledger()
        ledger.insert("P", "USD", 100, 0, 10, revision=1, known_at=1)
        before = ledger.query("P", "USD", 5, 3)   # 知悉3 < 撤销知悉6
        ledger.query("P", "USD", 5, 9)            # 受影响
        self.assertEqual(ledger.cache_size, 2)
        ledger.revoke("P", "USD", 1, known_at=6)
        self.assertEqual(ledger.cache_size, 1)
        self.assertIs(ledger.query("P", "USD", 5, 3), before)
        self.assertIsNone(ledger.query("P", "USD", 5, 9).amount)


class PersistenceTest(unittest.TestCase):
    def test_dump_load_roundtrip(self):
        ledger = make_acceptance_ledger()
        ledger.revoke("SKU1", "USD", 2, known_at=9)
        restored = Ledger.load(json.loads(json.dumps(ledger.dump())))
        self.assertEqual(restored.query("SKU1", "USD", 7, 7).amount, 10)
        self.assertEqual(restored.query("SKU1", "USD", 7, 8).amount, 15)
        self.assertEqual(restored.query("SKU1", "USD", 7, 9).amount, 10)


class CliTest(unittest.TestCase):
    def run_cli(self, *args, db):
        cmd = [sys.executable, "bitemporal_ledger.py", "--db", db, *args]
        return subprocess.run(cmd, capture_output=True, text=True)

    def test_cli_acceptance_flow(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = os.path.join(tmp, "ledger.json")
            r = self.run_cli("insert", "--product", "SKU1", "--currency", "USD",
                             "--amount", "10", "--valid-from", "0",
                             "--valid-to", "20", "--revision", "1",
                             "--known-at", "2", db=db)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertEqual(json.loads(r.stdout)["known_at"], 2)

            r = self.run_cli("insert", "--product", "SKU1", "--currency", "USD",
                             "--amount", "15", "--valid-from", "5",
                             "--valid-to", "10", "--revision", "2",
                             "--known-at", "8", db=db)
            self.assertEqual(r.returncode, 0, r.stderr)

            cases = [("7", "7", 10), ("7", "8", 15), ("10", "8", 10)]
            for bt, kt, expected in cases:
                r = self.run_cli("query", "--product", "SKU1", "--currency",
                                 "USD", "--business-time", bt,
                                 "--knowledge-time", kt, db=db)
                self.assertEqual(r.returncode, 0, r.stderr)
                out = json.loads(r.stdout)
                self.assertEqual(out["amount"], expected)
                self.assertIn("evidence", out)

    def test_cli_conflict_exit_code(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = os.path.join(tmp, "ledger.json")
            self.run_cli("insert", "--product", "P", "--currency", "USD",
                         "--amount", "100", "--valid-from", "0",
                         "--valid-to", "10", "--revision", "1",
                         "--known-at", "1", db=db)
            r = self.run_cli("insert", "--product", "P", "--currency", "USD",
                             "--amount", "101", "--valid-from", "0",
                             "--valid-to", "10", "--revision", "1",
                             "--known-at", "2", db=db)
            self.assertEqual(r.returncode, 1)
            self.assertIn("error", json.loads(r.stderr))


if __name__ == "__main__":
    unittest.main()
