import unittest

from txguard import (
    MISSING,
    ChangeOp,
    ConflictKind,
    Engine,
    Transaction,
    TxStatus,
)


def tx(tx_id, seq, *ops):
    return Transaction(tx_id=tx_id, commit_seq=seq, ops=tuple(ops))


def op(table="t", pk="k", before=None, after=None):
    return ChangeOp(table=table, pk=pk, before=before or {}, after=after or {})


class PartialBeforeTest(unittest.TestCase):
    def test_only_provided_fields_validated(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"a": 1, "b": 2})))
        # before 只提供 a，b 不参与校验
        result = engine.submit(tx("t2", 2, op(before={"a": 1}, after={"a": 9})))
        self.assertIs(result.status, TxStatus.COMMITTED)
        self.assertEqual(engine.store.get(("t", "k")), {"a": 9, "b": 2})

    def test_unprovided_after_fields_keep_current(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"a": 1, "b": 2})))
        engine.submit(tx("t2", 2, op(before={"a": 1}, after={"a": 5})))
        self.assertEqual(engine.store.get(("t", "k"))["b"], 2)

    def test_missing_field_is_not_null(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"a": 1})))
        # 字段 b 不存在；before 声明 b=None 不等于“缺失”
        result = engine.submit(tx("t2", 2, op(before={"b": None}, after={"a": 2})))
        self.assertIs(result.status, TxStatus.FAILED_CONFLICT)
        evidence = result.conflicts[0].fields[0]
        self.assertIs(evidence.actual, MISSING)
        self.assertIsNone(evidence.expected)
        # 显式写入 null 后则可匹配
        engine.resolve()
        engine.submit(tx("t3", 3, op(after={"b": None})))
        result = engine.submit(tx("t4", 4, op(before={"b": None}, after={"a": 3})))
        self.assertIs(result.status, TxStatus.COMMITTED)


class DeclaredOrderTest(unittest.TestCase):
    def test_same_key_validated_in_declared_order(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"v": 1})))
        # 同事务两次改同键：第二op校验的是第一op之后的状态
        result = engine.submit(tx("t2", 2,
                                  op(before={"v": 1}, after={"v": 2}),
                                  op(before={"v": 2}, after={"v": 3})))
        self.assertIs(result.status, TxStatus.COMMITTED)
        self.assertEqual(engine.store.field_value(("t", "k"), "v"), 3)

    def test_stale_chained_op_fails_whole_tx(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"v": 1})))
        # 第二op仍对旧快照取值 => 冲突，且第一op不得生效（原子性）
        result = engine.submit(tx("t2", 2,
                                  op(before={"v": 1}, after={"v": 2}),
                                  op(before={"v": 1}, after={"v": 9})))
        self.assertIs(result.status, TxStatus.FAILED_CONFLICT)
        self.assertEqual(engine.store.field_value(("t", "k"), "v"), 1)


class AtomicityTest(unittest.TestCase):
    def test_multi_key_tx_rolls_back_on_any_conflict(self):
        engine = Engine()
        engine.submit(tx("t1", 1,
                         op(pk="x", after={"v": 1}),
                         op(pk="y", after={"v": 1})))
        result = engine.submit(tx("t2", 2,
                                  op(pk="x", before={"v": 1}, after={"v": 2}),
                                  op(pk="y", before={"v": 999}, after={"v": 2})))
        self.assertIs(result.status, TxStatus.FAILED_CONFLICT)
        # 整体失败：x 也不得被改写
        self.assertEqual(engine.store.field_value(("t", "x"), "v"), 1)
        self.assertEqual(engine.store.field_value(("t", "y"), "v"), 1)
        # 事务触及的键都被隔离
        self.assertEqual(engine.quarantined, {("t", "x"), ("t", "y")})


class IsolationTest(unittest.TestCase):
    def test_independent_keys_continue_while_conflicted_key_isolated(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(pk="hot", after={"v": 1})))
        failed = engine.submit(tx("t2", 2, op(pk="hot", before={"v": 42}, after={"v": 2})))
        self.assertIs(failed.status, TxStatus.FAILED_CONFLICT)
        # 独立键事务照常提交
        ok = engine.submit(tx("t3", 3, op(pk="cold", after={"v": 7})))
        self.assertIs(ok.status, TxStatus.COMMITTED)
        # 同键后续事件挂起等待解决，而不是盲目后写覆盖
        held = engine.submit(tx("t4", 4, op(pk="hot", before={"v": 1}, after={"v": 5})))
        self.assertIs(held.status, TxStatus.HELD)
        self.assertEqual(engine.store.field_value(("t", "hot"), "v"), 1)

    def test_held_tx_reprocessed_after_resolution(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(pk="hot", after={"v": 1})))
        engine.submit(tx("t2", 2, op(pk="hot", before={"v": 42}, after={"v": 2})))
        engine.submit(tx("t3", 3, op(pk="hot", before={"v": 1}, after={"v": 5})))
        results = engine.resolve(("t", "hot"))
        self.assertEqual([r.status for r in results], [TxStatus.COMMITTED])
        self.assertEqual(engine.store.field_value(("t", "hot"), "v"), 5)
        self.assertFalse(engine.quarantined)

    def test_held_tx_stays_held_if_still_conflicting(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(pk="hot", after={"v": 1})))
        engine.submit(tx("t2", 2, op(pk="hot", before={"v": 42}, after={"v": 2})))
        engine.submit(tx("t3", 3, op(pk="hot", before={"v": 77}, after={"v": 5})))
        results = engine.resolve(("t", "hot"))
        self.assertEqual([r.status for r in results], [TxStatus.FAILED_CONFLICT])
        self.assertIn(("t", "hot"), engine.quarantined)


class ReferenceModelTest(unittest.TestCase):
    """全量参考模型：交换键、重复提交、迟到旧版本。"""

    def test_swap_keys(self):
        engine = Engine()
        engine.submit(tx("t1", 1,
                         op(pk="x", after={"owner": "alice"}),
                         op(pk="y", after={"owner": "bob"})))
        result = engine.submit(tx("t2", 2,
                                  op(pk="x", before={"owner": "alice"}, after={"owner": "bob"}),
                                  op(pk="y", before={"owner": "bob"}, after={"owner": "alice"})))
        self.assertIs(result.status, TxStatus.COMMITTED)
        self.assertEqual(engine.store.field_value(("t", "x"), "owner"), "bob")
        self.assertEqual(engine.store.field_value(("t", "y"), "owner"), "alice")

    def test_duplicate_submission_is_idempotent(self):
        engine = Engine()
        first = tx("t1", 1, op(before={}, after={"v": 1}))
        self.assertIs(engine.submit(first).status, TxStatus.COMMITTED)
        dup = engine.submit(tx("t1", 2, op(before={}, after={"v": 1})))
        self.assertIs(dup.status, TxStatus.DUPLICATE)
        self.assertEqual(engine.store.version(("t", "k")), 1)

    def test_late_old_version_reports_stale_with_minimal_evidence(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"v": 1, "w": "keep"})))
        engine.submit(tx("t2", 2, op(before={"v": 1}, after={"v": 2})))
        # 迟到旧版本：before 匹配 v1 快照而非当前
        result = engine.submit(tx("t3", 3, op(before={"v": 1, "w": "keep"}, after={"v": 0})))
        self.assertIs(result.status, TxStatus.FAILED_CONFLICT)
        conflict = result.conflicts[0]
        self.assertIs(conflict.kind, ConflictKind.STALE_BEFORE)
        # 最小证据：只有 v 不匹配，w 不出现在证据里
        self.assertEqual([f.field for f in conflict.fields], ["v"])
        self.assertEqual(conflict.fields[0].expected, 1)
        self.assertEqual(conflict.fields[0].actual, 2)

    def test_unknown_before_reports_missing_event(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"v": 1})))
        # before 与任何已知版本都对不上 => 疑似缺事件
        result = engine.submit(tx("t2", 2, op(before={"v": 55}, after={"v": 2})))
        self.assertIs(result.conflicts[0].kind, ConflictKind.MISSING_EVENT)

    def test_before_on_absent_row_reports_row_missing(self):
        engine = Engine()
        result = engine.submit(tx("t1", 1, op(before={"v": 1}, after={"v": 2})))
        self.assertIs(result.conflicts[0].kind, ConflictKind.ROW_MISSING)

    def test_no_blind_last_write_wins(self):
        engine = Engine()
        engine.submit(tx("t1", 1, op(after={"v": 1})))
        engine.submit(tx("t2", 2, op(before={"v": 999}, after={"v": 2})))
        # 冲突不得覆盖：当前值保持 1
        self.assertEqual(engine.store.field_value(("t", "k"), "v"), 1)


if __name__ == "__main__":
    unittest.main()
