"""LeftOuterJoinView 的 unittest 测试套件（仅标准库）。

覆盖：
- NULL 扩展行的建立 / 首个匹配撤销 / 最后一个匹配删除恢复；
- 一对多（左一右二输出 2）、多对多重数；
- 删一条右匹配但仍有其它匹配时不补 NULL；
- 同事务左右联动只按最终一致结果发布（中间 NULL 摆动不外泄）；
- 同事务净抵消不发布；连接键迁移；提交序号排序；非法变更拒绝。
"""
from __future__ import annotations

import unittest
from collections import Counter

from fixtures import changelog, dept, emp
from view import Change, LeftOuterJoinView, TupleRow, ViewInvariantError

L = "left"
R = "right"
K = "k"


def lrow(pk: int, key: str, v: str = "L") -> dict:
    return {"pk": pk, K: key, "lv": v}


def rrow(pk: int, key: str, v: str = "R") -> dict:
    return {"pk": pk, K: key, "rv": v}


def matched(lpk: int, rpk: int, key: str, lv: str = "L", rv: str = "R") -> dict:
    return {
        "l_pk": lpk, "l_k": key, "l_lv": lv,
        "r_pk": rpk, "r_k": key, "r_rv": rv, "matched": True,
    }


def null_row(lpk: int, key: str, lv: str = "L") -> dict:
    return {"l_pk": lpk, "l_k": key, "l_lv": lv, "matched": False}


def t(d: dict) -> TupleRow:
    return tuple(sorted(d.items()))


def multiset(rows: list[dict]) -> Counter:
    return Counter(t(r) for r in rows)


class NullExtensionTests(unittest.TestCase):
    def setUp(self) -> None:
        self.view = LeftOuterJoinView(join_key=K, left_table=L, right_table=R)

    def test_left_without_match_publishes_null_row(self) -> None:
        pub = self.view.commit_txn(
            [Change("t1", L, 1, None, lrow(1, "a"), 1)]
        )
        self.assertEqual(pub.inserted, (t(null_row(1, "a")),))
        self.assertEqual(pub.deleted, ())
        self.assertEqual(self.view.counts(), multiset([null_row(1, "a")]))

    def test_first_match_withdraws_null_and_publishes_match(self) -> None:
        self.view.commit_txn([Change("t1", L, 1, None, lrow(1, "a"), 1)])
        pub = self.view.commit_txn(
            [Change("t2", R, 10, None, rrow(10, "a"), 2)]
        )
        self.assertEqual(pub.inserted, (t(matched(1, 10, "a")),))
        self.assertEqual(pub.deleted, (t(null_row(1, "a")),))
        self.assertEqual(self.view.counts(), multiset([matched(1, 10, "a")]))

    def test_last_match_deleted_restores_null(self) -> None:
        self.view.commit_txn([Change("t1", L, 1, None, lrow(1, "a"), 1)])
        self.view.commit_txn([Change("t2", R, 10, None, rrow(10, "a"), 2)])
        pub = self.view.commit_txn(
            [Change("t3", R, 10, rrow(10, "a"), None, 3)]
        )
        self.assertEqual(pub.inserted, (t(null_row(1, "a")),))
        self.assertEqual(pub.deleted, (t(matched(1, 10, "a")),))
        self.assertEqual(self.view.counts(), multiset([null_row(1, "a")]))

    def test_delete_one_of_several_matches_keeps_no_null(self) -> None:
        # 左一右二 -> 输出 2 行；删除其中一条右匹配后仍剩 1 条，不补 NULL
        self.view.commit_txn([Change("t1", L, 1, None, lrow(1, "a"), 1)])
        self.view.commit_txn([Change("t2", R, 10, None, rrow(10, "a"), 2)])
        self.view.commit_txn([Change("t3", R, 11, None, rrow(11, "a"), 3)])
        self.assertEqual(
            self.view.counts(),
            multiset([matched(1, 10, "a"), matched(1, 11, "a")]),
        )
        pub = self.view.commit_txn(
            [Change("t4", R, 10, rrow(10, "a"), None, 4)]
        )
        self.assertEqual(pub.inserted, ())
        self.assertEqual(pub.deleted, (t(matched(1, 10, "a")),))
        self.assertEqual(self.view.counts(), multiset([matched(1, 11, "a")]))

    def test_left_one_right_two_outputs_two_rows(self) -> None:
        self.view.commit_txn([Change("t1", L, 1, None, lrow(1, "a"), 1)])
        self.view.commit_txn([Change("t2", R, 10, None, rrow(10, "a"), 2)])
        pub = self.view.commit_txn(
            [Change("t3", R, 11, None, rrow(11, "a"), 3)]
        )
        self.assertEqual(pub.inserted, (t(matched(1, 11, "a")),))
        self.assertEqual(len(self.view.rows()), 2)


class MultiplicityTests(unittest.TestCase):
    def setUp(self) -> None:
        self.view = LeftOuterJoinView(join_key=K, left_table=L, right_table=R)

    def _seed(self, left_pks, right_pks) -> None:
        changes = [
            Change("seedL", L, pk, None, lrow(pk, "a"), 0) for pk in left_pks
        ]
        changes += [
            Change("seedR", R, 100 + pk, None, rrow(100 + pk, "a"), 0)
            for pk in right_pks
        ]
        self.view.commit_txn(changes, txn_id="seed", commit_seq=0)

    def test_two_left_two_right_outputs_four(self) -> None:
        # 多对多：2 x 2 = 4 行，每个 (左,右) 组合重数各 1
        self._seed([1, 2], [1, 2])
        self.assertEqual(len(self.view.rows()), 4)
        expected = multiset(
            [
                matched(1, 101, "a"), matched(1, 102, "a"),
                matched(2, 101, "a"), matched(2, 102, "a"),
            ]
        )
        self.assertEqual(self.view.counts(), expected)
        self.assertTrue(all(n == 1 for n in self.view.counts().values()))

    def test_remove_right_in_many_to_many_deletes_every_left_pair(self) -> None:
        self._seed([1, 2], [1, 2])
        pub = self.view.commit_txn(
            [Change("t", R, 101, rrow(101, "a"), None, 1)]
        )
        self.assertEqual(len(pub.deleted), 2)
        self.assertEqual(
            multiset([dict(d_) for d_ in pub.deleted]),
            multiset([matched(1, 101, "a"), matched(2, 101, "a")]),
        )
        self.assertEqual(len(self.view.rows()), 2)  # 剩 2 个左 x 1 个右
        self.assertFalse(any(not dict(r)["matched"] for r in self.view.rows()))

    def test_remove_left_when_matched_deletes_its_pairs_only(self) -> None:
        self._seed([1, 2], [1, 2])
        pub = self.view.commit_txn(
            [Change("t", L, 1, lrow(1, "a"), None, 1)]
        )
        self.assertEqual(len(pub.deleted), 2)
        self.assertEqual(
            multiset([dict(d_) for d_ in pub.deleted]),
            multiset([matched(1, 101, "a"), matched(1, 102, "a")]),
        )
        self.assertEqual(len(self.view.rows()), 2)
        # 另一个左行不受影响，不产生 NULL
        self.assertEqual(
            self.view.counts(),
            multiset([matched(2, 101, "a"), matched(2, 102, "a")]),
        )

    def test_insert_left_with_existing_matches_skips_null(self) -> None:
        self._seed([], [1])
        pub = self.view.commit_txn(
            [Change("t", L, 5, None, lrow(5, "a"), 1)]
        )
        self.assertEqual(pub.inserted, (t(matched(5, 101, "a")),))
        self.assertEqual(pub.deleted, ())


class SameTransactionTests(unittest.TestCase):
    def setUp(self) -> None:
        self.view = LeftOuterJoinView(join_key=K, left_table=L, right_table=R)

    def test_left_insert_and_first_match_same_txn_no_null_escapes(self) -> None:
        # 同事务：左行插入 + 首个右匹配插入 -> 只发布匹配行
        pub = self.view.commit_txn(
            [
                Change("t", L, 1, None, lrow(1, "a"), 1),
                Change("t", R, 10, None, rrow(10, "a"), 1),
            ]
        )
        self.assertEqual(pub.inserted, (t(matched(1, 10, "a")),))
        self.assertEqual(pub.deleted, ())

    def test_rename_left_and_remove_last_match_same_txn_net_null(self) -> None:
        # 初始：左行有一个匹配。同事务内左行改名 + 删除最后匹配：
        # 旧匹配行删除，发布“改名后”的新 NULL 行（绝不发布旧 NULL）
        self.view.commit_txn(
            [
                Change("s", L, 1, None, lrow(1, "a"), 0),
                Change("s", R, 10, None, rrow(10, "a"), 0),
            ],
            txn_id="s",
            commit_seq=0,
        )
        pub = self.view.commit_txn(
            [
                Change("t", L, 1, lrow(1, "a", "L"), lrow(1, "a", "L2"), 1),
                Change("t", R, 10, rrow(10, "a"), None, 1),
            ]
        )
        self.assertEqual(pub.inserted, (t(null_row(1, "a", "L2")),))
        self.assertEqual(pub.deleted, (t(matched(1, 10, "a")),))
        self.assertNotIn(t(null_row(1, "a", "L")), self.view.counts())

    def test_delete_and_reinsert_same_right_same_txn_publishes_nothing(self) -> None:
        self.view.commit_txn(
            [
                Change("s", L, 1, None, lrow(1, "a"), 0),
                Change("s", R, 10, None, rrow(10, "a"), 0),
            ],
            txn_id="s",
            commit_seq=0,
        )
        pub = self.view.commit_txn(
            [
                Change("t", R, 10, rrow(10, "a"), None, 1),
                Change("t", R, 10, None, rrow(10, "a"), 1),
            ]
        )
        self.assertEqual(pub.inserted, ())
        self.assertEqual(pub.deleted, ())
        self.assertEqual(self.view.counts(), multiset([matched(1, 10, "a")]))

    def test_replace_only_match_same_txn_swaps_without_null(self) -> None:
        # 同事务内删旧匹配、插新匹配：NULL 撤销/恢复相互抵消，只做匹配行交换
        self.view.commit_txn(
            [
                Change("s", L, 1, None, lrow(1, "a"), 0),
                Change("s", R, 10, None, rrow(10, "a"), 0),
            ],
            txn_id="s",
            commit_seq=0,
        )
        pub = self.view.commit_txn(
            [
                Change("t", R, 10, rrow(10, "a"), None, 1),
                Change("t", R, 11, None, rrow(11, "a"), 1),
            ]
        )
        self.assertEqual(pub.inserted, (t(matched(1, 11, "a")),))
        self.assertEqual(pub.deleted, (t(matched(1, 10, "a")),))
        self.assertEqual(self.view.counts(), multiset([matched(1, 11, "a")]))

    def test_right_key_migration_same_txn_moves_between_groups(self) -> None:
        # 员工 10 从键 a 调到键 b，同时部门 b 在同事务创建：
        # a 组恢复 NULL；b 组直接得到匹配行
        self.view.commit_txn(
            [
                Change("s", L, 1, None, lrow(1, "a"), 0),
                Change("s", R, 10, None, rrow(10, "a"), 0),
            ],
            txn_id="s",
            commit_seq=0,
        )
        pub = self.view.commit_txn(
            [
                Change("t", R, 10, rrow(10, "a"), rrow(10, "b"), 1),
                Change("t", L, 2, None, lrow(2, "b"), 1),
            ]
        )
        self.assertEqual(
            multiset([dict(i) for i in pub.inserted]),
            multiset([null_row(1, "a"), matched(2, 10, "b")]),
        )
        self.assertEqual(pub.deleted, (t(matched(1, 10, "a")),))
        self.assertEqual(
            self.view.counts(),
            multiset([null_row(1, "a"), matched(2, 10, "b")]),
        )


class DrainOrderingTests(unittest.TestCase):
    def setUp(self) -> None:
        self.view = LeftOuterJoinView(join_key=K, left_table=L, right_table=R)

    def test_ingest_out_of_order_drains_by_commit_seq(self) -> None:
        c1 = Change("late", L, 2, None, lrow(2, "b"), 2)
        c0 = Change("early", L, 1, None, lrow(1, "a"), 1)
        self.view.ingest([c1, c0])
        pubs = self.view.drain()
        self.assertEqual([p.commit_seq for p in pubs], [1, 2])
        self.assertEqual([p.txn_id for p in pubs], ["early", "late"])
        # 第二次 drain 没有遗留事务
        self.assertEqual(self.view.drain(), [])

    def test_duplicate_commit_seq_rejected(self) -> None:
        self.view.ingest(Change("x", L, 1, None, lrow(1, "a"), 1))
        self.view.ingest(Change("y", L, 2, None, lrow(2, "b"), 1))
        with self.assertRaises(ViewInvariantError):
            self.view.drain()

    def test_inconsistent_seq_within_txn_rejected(self) -> None:
        self.view.ingest(Change("t", L, 1, None, lrow(1, "a"), 1))
        with self.assertRaises(ViewInvariantError):
            self.view.ingest(Change("t", L, 1, lrow(1, "a"), None, 2))


class FixtureScenarioTests(unittest.TestCase):
    """用本地夹具 changelog 跑端到端时间线。"""

    def setUp(self) -> None:
        self.view = LeftOuterJoinView(
            join_key="dept_id", left_table="departments", right_table="employees"
        )
        self.pubs = {}
        for ch in changelog():
            if ch.txn_id not in self.pubs:
                self.pubs[ch.txn_id] = []
            self.pubs[ch.txn_id].append(ch)

    def _commit(self, txn_id: str):
        return self.view.commit_txn(self.pubs[txn_id])

    def test_timeline_null_first_match_multiplicity_and_restore(self) -> None:
        p1 = self._commit("T1")
        self.assertEqual(len(p1.inserted), 1)
        self.assertFalse(dict(p1.inserted[0])["matched"])

        p2 = self._commit("T2")  # 首个匹配撤销 NULL
        self.assertEqual(len(p2.inserted), 1)
        self.assertEqual(len(p2.deleted), 1)
        self.assertFalse(dict(p2.deleted[0])["matched"])

        p3 = self._commit("T3")  # 左一右二输出 2
        self.assertEqual(len(p3.inserted), 1)
        self.assertEqual(len(self.view.rows()), 2)

        p4 = self._commit("T4")  # 删一右仍 1 不补 NULL；HR 直接匹配
        self.assertTrue(all(dict(i)["matched"] for i in p4.inserted))
        self.assertEqual(len(p4.deleted), 1)
        eng_rows = [
            r for r in self.view.rows() if dict(r)["l_dept_id"] == "ENG"
        ]
        self.assertEqual(len(eng_rows), 1)
        self.assertTrue(dict(eng_rows[0])["matched"])  # 没补 NULL

        p5 = self._commit("T5")  # 最后一个匹配删除 -> 恢复 NULL
        self.assertTrue(any(not dict(i)["matched"] for i in p5.inserted))

    def test_t6_left_right_same_txn_final_state_only(self) -> None:
        for txn in ("T1", "T2", "T3", "T4", "T5"):
            self._commit(txn)
        p6 = self._commit("T6")  # 部门改名 + 首个员工同事务
        # 只发布“改名后”的匹配行，并删除旧 NULL；旧 NULL 与新 NULL 摆动不外泄
        self.assertEqual(len(p6.inserted), 1)
        self.assertTrue(dict(p6.inserted[0])["matched"])
        self.assertEqual(dict(p6.inserted[0])["l_dname"], "Engineering Team")
        self.assertEqual(len(p6.deleted), 1)
        self.assertFalse(dict(p6.deleted[0])["matched"])
        self.assertEqual(
            len([r for r in self.view.rows() if not dict(r)["matched"]]), 0
        )

    def test_t8_cancellation_publishes_nothing(self) -> None:
        for txn in ("T1", "T2", "T3", "T4", "T5", "T6", "T7"):
            self._commit(txn)
        before = self.view.counts()
        p8 = self._commit("T8")  # 同事务删后插，净抵消
        self.assertEqual(p8.inserted, ())
        self.assertEqual(p8.deleted, ())
        self.assertEqual(self.view.counts(), before)

    def test_t9_delete_with_remaining_match_no_null(self) -> None:
        for txn in ("T1", "T2", "T3", "T4", "T5", "T6", "T7", "T8"):
            self._commit(txn)
        p9 = self._commit("T9")
        # SALES 删除 Carol 后仍有 Frank -> 删 1 行、加 1 行，绝不恢复 NULL
        self.assertEqual(len(p9.inserted), 1)
        self.assertEqual(len(p9.deleted), 1)
        self.assertTrue(all(dict(r)["matched"] for r in p9.inserted))
        sales = [r for r in self.view.rows() if dict(r)["l_dept_id"] == "SALES"]
        self.assertEqual(len(sales), 1)
        self.assertTrue(dict(sales[0])["matched"])
        self.assertEqual(dict(sales[0])["r_ename"], "Frank")

    def test_final_view_matches_expected_left_outer_join(self) -> None:
        for txn in sorted(self.pubs, key=lambda t: self.pubs[t][0].commit_seq):
            self._commit(txn)
        got = {
            (dict(r)["l_dept_id"], dict(r).get("r_ename"))
            for r in self.view.rows()
        }
        self.assertEqual(
            got,
            {("ENG", "Dan"), ("HR", None), ("SALES", "Frank")},
        )


class ValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.view = LeftOuterJoinView(join_key=K, left_table=L, right_table=R)

    def test_unknown_table_rejected(self) -> None:
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", "other", 1, None, {"pk": 1, K: "a"}, 1)])

    def test_missing_join_key_rejected(self) -> None:
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", L, 1, None, {"pk": 1}, 1)])

    def test_pk_mismatch_rejected(self) -> None:
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", L, 1, None, lrow(2, "a"), 1)])

    def test_both_before_after_none_rejected(self) -> None:
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", L, 1, None, None, 1)])

    def test_delete_missing_row_rejected(self) -> None:
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", L, 1, lrow(1, "a"), None, 1)])

    def test_before_value_mismatch_rejected(self) -> None:
        self.view.commit_txn([Change("t", L, 1, None, lrow(1, "a", "X"), 1)])
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", L, 1, lrow(1, "a", "Y"), None, 2)])

    def test_duplicate_pk_insert_rejected(self) -> None:
        self.view.commit_txn([Change("t", L, 1, None, lrow(1, "a"), 1)])
        with self.assertRaises(ViewInvariantError):
            self.view.commit_txn([Change("t", L, 1, None, lrow(1, "a"), 2)])

    def test_empty_txn_raises_indexerror_guarded_by_api(self) -> None:
        # commit_txn 需要至少一条变更来确定 txn_id/commit_seq
        with self.assertRaises(IndexError):
            self.view.commit_txn([])


if __name__ == "__main__":
    unittest.main(verbosity=2)
