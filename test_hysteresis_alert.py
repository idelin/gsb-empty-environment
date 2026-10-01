"""hysteresis_alert 的 unittest 测试。所有数据均为合成数据。"""
import json
import os
import tempfile
import unittest

from hysteresis_alert import (
    AlertMachine,
    Config,
    RejectError,
    load_machine,
    save_machine,
)


def make_machine(high=10.0, low=5.0, m=2, n=2):
    return AlertMachine(Config(high=high, low=low, m=m, n=n))


class TestAcceptanceBoundary(unittest.TestCase):
    """验收边界：H10 L5 m2 n2。"""

    def test_full_acceptance_sequence(self):
        machine = make_machine()
        # 11, 缺测, 11 -> 不触发
        self.assertIsNone(machine.ingest(1, 11.0))
        self.assertIsNone(machine.ingest(2, None))
        self.assertIsNone(machine.ingest(3, 11.0))
        self.assertFalse(machine.state.alerted)
        # 再 11 -> 触发
        event = machine.ingest(4, 11.0)
        self.assertIsNotNone(event)
        self.assertEqual(event["event"], "ALERT")
        self.assertEqual(event["seq"], 4)
        self.assertTrue(machine.state.alerted)
        # 4, 7, 4 -> 不解除
        self.assertIsNone(machine.ingest(5, 4.0))
        self.assertIsNone(machine.ingest(6, 7.0))
        self.assertIsNone(machine.ingest(7, 4.0))
        self.assertTrue(machine.state.alerted)
        # 再 4 -> 解除
        event = machine.ingest(8, 4.0)
        self.assertIsNotNone(event)
        self.assertEqual(event["event"], "RESOLVE")
        self.assertEqual(event["seq"], 8)
        self.assertFalse(machine.state.alerted)


class TestHysteresisRules(unittest.TestCase):
    def test_intermediate_value_resets_both_counters(self):
        machine = make_machine()
        machine.ingest(1, 11.0)   # high_count=1
        machine.ingest(2, 7.0)    # 中间值，重置
        self.assertEqual(machine.state.high_count, 0)
        self.assertIsNone(machine.ingest(3, 11.0))  # high_count=1，不触发
        event = machine.ingest(4, 11.0)             # high_count=2
        self.assertEqual(event["event"], "ALERT")

    def test_missing_resets_counters(self):
        machine = make_machine()
        machine.ingest(1, 11.0)
        machine.ingest(2, None)
        self.assertEqual(machine.state.high_count, 0)
        self.assertEqual(machine.state.low_count, 0)

    def test_boundary_values_inclusive(self):
        machine = make_machine(m=1, n=1)
        event = machine.ingest(1, 10.0)  # 恰好等于 H 即触发
        self.assertEqual(event["event"], "ALERT")
        event = machine.ingest(2, 5.0)   # 恰好等于 L 即解除
        self.assertEqual(event["event"], "RESOLVE")

    def test_no_duplicate_events_while_alerted(self):
        machine = make_machine()
        self.assertIsNone(machine.ingest(1, 11.0))
        self.assertEqual(machine.ingest(2, 11.0)["event"], "ALERT")
        # 告警中继续高出阈值不再重复发 ALERT
        self.assertIsNone(machine.ingest(3, 12.0))
        self.assertIsNone(machine.ingest(4, 13.0))
        self.assertEqual(len(machine.state.events), 1)

    def test_low_values_before_alert_do_nothing(self):
        machine = make_machine()
        self.assertIsNone(machine.ingest(1, 4.0))
        self.assertIsNone(machine.ingest(2, 4.0))
        self.assertFalse(machine.state.alerted)
        self.assertEqual(machine.state.events, [])


class TestSequenceIdempotency(unittest.TestCase):
    def test_same_seq_same_value_idempotent(self):
        machine = make_machine()
        machine.ingest(1, 11.0)
        snapshot = (machine.state.high_count, machine.state.last_seq)
        self.assertIsNone(machine.ingest(1, 11.0))  # 幂等重放
        self.assertEqual((machine.state.high_count, machine.state.last_seq), snapshot)

    def test_same_seq_same_missing_idempotent(self):
        machine = make_machine()
        machine.ingest(1, None)
        self.assertIsNone(machine.ingest(1, None))

    def test_same_seq_different_value_rejected(self):
        machine = make_machine()
        machine.ingest(1, 11.0)
        with self.assertRaises(RejectError):
            machine.ingest(1, 12.0)
        # 拒绝后状态未被污染
        self.assertEqual(machine.state.last_value, 11.0)
        self.assertEqual(machine.state.high_count, 1)

    def test_missing_vs_value_same_seq_rejected(self):
        machine = make_machine()
        machine.ingest(1, None)
        with self.assertRaises(RejectError):
            machine.ingest(1, 11.0)

    def test_out_of_order_seq_rejected(self):
        machine = make_machine()
        machine.ingest(5, 11.0)
        with self.assertRaises(RejectError):
            machine.ingest(4, 11.0)


class TestConfigSwitch(unittest.TestCase):
    def test_config_switch_resets_counters_and_affects_future_only(self):
        machine = make_machine()
        machine.ingest(1, 11.0)  # high_count=1
        machine.apply_config(Config(high=20.0, low=2.0, m=2, n=2))
        self.assertEqual(machine.state.high_count, 0)
        self.assertEqual(machine.state.low_count, 0)
        # 旧配置下 11 会累计，新配置下 11 < H=20 属于中间值
        self.assertIsNone(machine.ingest(2, 11.0))
        self.assertIsNone(machine.ingest(3, 11.0))
        self.assertFalse(machine.state.alerted)
        # 新阈值下 21 连续 2 次触发
        self.assertIsNone(machine.ingest(4, 21.0))
        self.assertEqual(machine.ingest(5, 21.0)["event"], "ALERT")

    def test_config_switch_keeps_alert_state(self):
        machine = make_machine()
        machine.ingest(1, 11.0)
        machine.ingest(2, 11.0)
        self.assertTrue(machine.state.alerted)
        machine.apply_config(Config(high=10.0, low=5.0, m=2, n=2))
        self.assertTrue(machine.state.alerted)  # 告警状态保留，仅重置计数

    def test_invalid_config_rejected(self):
        with self.assertRaises(ValueError):
            AlertMachine(Config(high=5.0, low=5.0, m=2, n=2))  # L 须 < H
        with self.assertRaises(ValueError):
            AlertMachine(Config(high=10.0, low=5.0, m=0, n=2))


class TestPersistence(unittest.TestCase):
    def test_restart_does_not_resend_state_changes(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            machine = make_machine()
            machine.ingest(1, 11.0)
            event = machine.ingest(2, 11.0)
            self.assertEqual(event["event"], "ALERT")
            save_machine(machine, path)

            # 模拟重启：从磁盘恢复，继续处理，不应重发 ALERT
            restored = load_machine(path)
            self.assertTrue(restored.state.alerted)
            self.assertIsNone(restored.ingest(3, 12.0))
            self.assertEqual(len(restored.state.events), 1)
            # 解除流程在重启后依然正确
            save_machine(restored, path)
            restored2 = load_machine(path)
            self.assertIsNone(restored2.ingest(4, 4.0))
            self.assertEqual(restored2.ingest(5, 4.0)["event"], "RESOLVE")

    def test_restart_preserves_last_seq_for_idempotency(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            machine = make_machine()
            machine.ingest(7, 11.0)
            save_machine(machine, path)
            restored = load_machine(path)
            self.assertIsNone(restored.ingest(7, 11.0))  # 重启后重放仍幂等
            with self.assertRaises(RejectError):
                restored.ingest(7, 99.0)

    def test_state_file_is_valid_json(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "state.json")
            machine = make_machine()
            machine.ingest(1, 11.0)
            save_machine(machine, path)
            with open(path, encoding="utf-8") as f:
                data = json.load(f)
            self.assertEqual(data["config"], {"high": 10.0, "low": 5.0, "m": 2, "n": 2})
            self.assertEqual(data["last_seq"], 1)
            self.assertFalse(os.path.exists(path + ".tmp"))  # 原子写无残留

    def test_load_missing_file_returns_none(self):
        self.assertIsNone(load_machine("/nonexistent/path/state.json"))


if __name__ == "__main__":
    unittest.main()
