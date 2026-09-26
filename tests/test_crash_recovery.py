"""提交边界故障枚举与崩溃恢复。

故障点（见 ledger.py 文档）：
1. commit:before_txn        —— 事务开始前崩溃：无持久化，重试正常完成。
2. commit:before_db_commit  —— 写入后落库前崩溃：整体回滚，无半提交状态。
3. commit:after_db_commit   —— 落库后返回前崩溃：重试必须复用原噪声与预算记录。
"""

import os
import random
import tempfile
import unittest

from dpledger import BudgetLedger, verify

CONTENT = {"type": "count", "dataset": "synthetic", "sensitivity": 1.0}


class SimulatedCrash(Exception):
    pass


def crash():
    raise SimulatedCrash("injected crash")


class CrashRecoveryTestBase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "ledger.db")

    def tearDown(self):
        self.tmp.cleanup()

    def open_ledger(self, fault_hooks=None):
        # 每次用同一 db 文件重新打开，模拟进程崩溃后重启。
        return BudgetLedger(self.db, total_epsilon=4.0,
                            rng=random.Random(99), fault_hooks=fault_hooks)


class TestCrashBeforeTxn(CrashRecoveryTestBase):
    def test_crash_before_txn_leaves_nothing(self):
        ledger = self.open_ledger(fault_hooks={"commit:before_txn": crash})
        ledger.reserve("q1", CONTENT, 1.0)
        with self.assertRaises(SimulatedCrash):
            ledger.commit("q1", CONTENT, true_value=10.0)
        ledger.close()

        ledger = self.open_ledger()  # 重启后重试
        self.assertEqual(ledger.status("q1")["state"], "RESERVED")
        out = ledger.commit("q1", CONTENT, true_value=10.0)
        self.assertEqual(out["state"], "PUBLISHED")
        self.assertEqual(ledger.budget()["spent"], 1.0)
        ledger.close()


class TestCrashBeforeDbCommit(CrashRecoveryTestBase):
    def test_crash_before_db_commit_rolls_back(self):
        ledger = self.open_ledger(
            fault_hooks={"commit:before_db_commit": crash})
        ledger.reserve("q1", CONTENT, 1.0)
        with self.assertRaises(SimulatedCrash):
            ledger.commit("q1", CONTENT, true_value=10.0)
        ledger.close()

        ledger = self.open_ledger()
        # 回滚后无半提交状态：仍为 RESERVED，无结果记录，预算未扣。
        self.assertEqual(ledger.status("q1")["state"], "RESERVED")
        self.assertEqual(ledger.budget()["spent"], 0.0)
        self.assertEqual(
            [e for e in ledger.events() if e["type"] == "PUBLISH"], [])
        out = ledger.commit("q1", CONTENT, true_value=10.0)
        self.assertEqual(out["state"], "PUBLISHED")
        ledger.close()


class TestCrashAfterDbCommit(CrashRecoveryTestBase):
    def test_crash_after_db_commit_retry_reuses_records(self):
        ledger = self.open_ledger(
            fault_hooks={"commit:after_db_commit": crash})
        ledger.reserve("q1", CONTENT, 1.0)
        with self.assertRaises(SimulatedCrash):
            ledger.commit("q1", CONTENT, true_value=10.0)
        # 崩溃发生在落库之后：结果与预算记录已持久化。
        persisted = ledger.status("q1")
        self.assertEqual(persisted["state"], "PUBLISHED")
        ledger.close()

        ledger = self.open_ledger()  # 重启后重试同一提交
        retry = ledger.commit("q1", CONTENT, true_value=10.0)
        self.assertTrue(retry["reused"])
        # 复用原噪声：与公开日志中发布的值一致。
        publishes = [e for e in ledger.events() if e["type"] == "PUBLISH"]
        self.assertEqual(len(publishes), 1)
        self.assertEqual(retry["noisy_value"],
                         publishes[0]["payload"]["noisy_value"])
        # 预算只扣减一次。
        self.assertEqual(ledger.budget()["spent"], 1.0)
        # 独立状态机核对：守恒 + 最多一次发布。
        report = verify(ledger.events(), total_epsilon=4.0)
        self.assertEqual(report["spent"], 1.0)
        ledger.close()


class TestCrashBetweenReserveAndCommit(CrashRecoveryTestBase):
    def test_reservation_survives_restart(self):
        ledger = self.open_ledger()
        ledger.reserve("q1", CONTENT, 2.0)
        ledger.close()  # 模拟崩溃

        ledger = self.open_ledger()
        self.assertEqual(ledger.status("q1")["state"], "RESERVED")
        self.assertEqual(ledger.budget()["reserved"], 2.0)
        out = ledger.commit("q1", CONTENT, true_value=5.0)
        self.assertEqual(out["state"], "PUBLISHED")
        ledger.close()


if __name__ == "__main__":
    unittest.main()
