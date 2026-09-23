import unittest

from cdc import (
    TABLES,
    ArrivalOrderProcessor,
    generate,
    run_case,
    shrink,
    validate_stream,
)
from cdc.events import Change, Commit, Begin
from cdc.shrinker import _drop_txn


def txn_structure(stream):
    """每个事务的事件类型序列: 用于检查事务结构保持。"""
    structure = {}
    for ev in stream:
        txn_id = getattr(ev, "txn_id", None)
        if txn_id is None:
            continue
        structure.setdefault(txn_id, []).append(type(ev).__name__)
    return structure


class ShrinkerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.generated = generate(seed=0)
        cls.base = run_case(cls.generated.stream, TABLES, ArrivalOrderProcessor)
        assert cls.base.status == "mismatch", "演示种子应使缺陷处理器失败"

    def test_shrunk_stream_still_fails_same_way(self):
        shrunk = shrink(self.generated.stream, TABLES, ArrivalOrderProcessor)
        result = run_case(shrunk, TABLES, ArrivalOrderProcessor)
        self.assertEqual("mismatch", result.status)

    def test_shrunk_stream_is_smaller(self):
        shrunk = shrink(self.generated.stream, TABLES, ArrivalOrderProcessor)
        self.assertLess(len(shrunk), len(self.generated.stream))

    def test_shrunk_stream_remains_valid_and_replayable(self):
        shrunk = shrink(self.generated.stream, TABLES, ArrivalOrderProcessor)
        validate_stream(shrunk, TABLES)  # 结构有效
        for txn_id, kinds in txn_structure(shrunk).items():
            self.assertEqual("Begin", kinds[0], f"事务 {txn_id} 以 Begin 开始")
            self.assertIn(kinds[-1], ("Commit", "Abort"),
                          f"事务 {txn_id} 以终结事件结束")
        # 可重放: 正确处理器跑缩减后的流仍与 oracle 一致
        self.assertEqual("ok", run_case(shrunk, TABLES).status)

    def test_shrunk_stream_is_minimal_wrt_txn_removal(self):
        shrunk = shrink(self.generated.stream, TABLES, ArrivalOrderProcessor)
        txn_ids = {ev.txn_id for ev in shrunk
                   if getattr(ev, "txn_id", None) is not None}
        for txn_id in txn_ids:
            candidate = _drop_txn(shrunk, txn_id)
            result = run_case(candidate, TABLES, ArrivalOrderProcessor)
            self.assertNotEqual("mismatch", result.status,
                                f"删除事务 {txn_id} 后不应再失败(1-极小性)")

    def test_shrink_requires_failing_stream(self):
        ok_stream = [Begin(0), Change(0, 0, "users", "u1", None, None), Commit(0, 0)]
        with self.assertRaises(ValueError):
            shrink(ok_stream, TABLES)


if __name__ == "__main__":
    unittest.main()
