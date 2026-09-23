import unittest

from cdc import TABLES, ArrivalOrderProcessor, freeze_row, run_case
from cdc.events import Begin, Change, Commit, StreamValidationError

ROW_A = freeze_row({"balance": 10})
ROW_B = freeze_row({"balance": 20})


def reordered_stream():
    """结构有效, 但乱序会让 ArrivalOrderProcessor 出错。"""
    return [
        Begin(0),
        Change(0, 1, "users", "u1", ROW_A, ROW_B),  # seq1 先到
        Change(0, 0, "users", "u1", None, ROW_A),
        Commit(0, 0),
    ]


class HarnessClassificationTest(unittest.TestCase):
    def test_ok(self):
        stream = [Begin(0), Change(0, 0, "users", "u1", None, ROW_A), Commit(0, 0)]
        self.assertEqual("ok", run_case(stream, TABLES).status)

    def test_mismatch(self):
        result = run_case(reordered_stream(), TABLES, ArrivalOrderProcessor)
        self.assertEqual("mismatch", result.status)
        self.assertEqual(
            [("users", "u1", ROW_B, ROW_A)],  # 期望 ROW_B, 实际 ROW_A
            result.diffs,
        )

    def test_invalid_stream_separated(self):
        bad = [Begin(0), Change(0, 0, "未知表", "x", None, ROW_A), Commit(0, 0)]
        result = run_case(bad, TABLES)
        self.assertEqual("invalid_stream", result.status)
        self.assertIsInstance(result.error, StreamValidationError)

    def test_processor_error_separated(self):
        # 流结构有效(校验通过), 但处理器发现语义冲突
        conflict = [
            Begin(0),
            Change(0, 0, "users", "u1", None, ROW_A),
            Change(0, 0, "users", "u1", None, ROW_B),
            Commit(0, 0),
        ]
        result = run_case(conflict, TABLES)
        self.assertEqual("processor_error", result.status)

    def test_invalid_stream_never_reaches_processor(self):
        # 无效流同时含有处理器会报错的内容; 应归类为 invalid_stream
        bad = [Begin(0), Change(0, 0, "未知表", "x", None, ROW_A), Commit(0, 0)]
        result = run_case(bad, TABLES, ArrivalOrderProcessor)
        self.assertEqual("invalid_stream", result.status)


if __name__ == "__main__":
    unittest.main()
