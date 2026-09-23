"""变更驱动的增量左外连接视图（Python 3.11+，仅标准库）。

输入：本地 Change 夹具（事务ID、表、主键、前值/后值、提交序号），
     * 不接任何消息平台 *。
输出：按事务边界发布的 Publication（插入/删除的多重集增量），
     并在内部维护左外连接视图的当前多重集。

关键语义：
- 左行先到且右侧无匹配 -> 发布一条 NULL 扩展行；
- 首个右匹配到来 -> 撤销该 NULL 扩展行，发布匹配行；
- 右匹配多于一条 -> 每个 (左, 右) 组合各产生一行，重数正确；
- 删除右匹配后仍有其它匹配 -> 不补 NULL；最后一个匹配删除 -> 恢复 NULL 行；
- 同一事务内左表与右表的多次变化只按“提交后的最终一致状态”发布一次净增量。
"""
from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field
from typing import Optional

Row = dict
TupleRow = tuple[tuple[str, object], ...]


@dataclass(frozen=True)
class Change:
    """单行表变更。before=None 表示插入；after=None 表示删除；都有值表示更新。"""

    txn_id: str
    table: str
    pk: object
    before: Optional[Row]
    after: Optional[Row]
    commit_seq: int


@dataclass(frozen=True)
class Publication:
    """一个事务提交后对外发布的净增量（多重集；同型行可重复出现）。"""

    txn_id: str
    commit_seq: int
    inserted: tuple[TupleRow, ...]
    deleted: tuple[TupleRow, ...]


class ViewInvariantError(ValueError):
    """变更与引擎当前状态不一致（缺行、重复主键、未知表、键缺失等）。"""


def _sorted_tuple(row: Row) -> TupleRow:
    return tuple(sorted(row.items()))


class LeftOuterJoinView:
    def __init__(
        self,
        join_key: str = "k",
        left_table: str = "left",
        right_table: str = "right",
    ) -> None:
        self.join_key = join_key
        self.left_table = left_table
        self.right_table = right_table
        self._left: dict[object, Row] = {}
        self._right: dict[object, Row] = {}
        # join key -> Counter(主键 -> 重数)，允许同键多行（多对多重数）
        self._left_by_key: dict[object, Counter] = {}
        self._right_by_key: dict[object, Counter] = {}
        # 当前左外连接视图：输出元组 -> 重数
        self._multiset: Counter[TupleRow] = Counter()
        # 尚未按提交序号排空的事务
        self._pending: dict[str, tuple[int, list[Change]]] = {}
        self.publications: list[Publication] = []

    # ------------------------------------------------------------------ #
    # 公共 API
    # ------------------------------------------------------------------ #
    def ingest(self, changes: list[Change] | Change) -> None:
        """接收一批本地夹具变更，按事务ID缓存，等待 drain 按提交序号发布。"""
        if isinstance(changes, Change):
            changes = [changes]
        for ch in changes:
            self._validate_change(ch)
            seq, bucket = self._pending.setdefault(
                ch.txn_id, (ch.commit_seq, [])
            )
            if seq != ch.commit_seq:
                raise ViewInvariantError(
                    f"事务 {ch.txn_id} 的提交序号不一致: {seq} != {ch.commit_seq}"
                )
            bucket.append(ch)

    def drain(self) -> list[Publication]:
        """按 commit_seq 升序提交所有已缓存事务，返回本次发布列表。"""
        seqs = [seq for seq, _ in self._pending.values()]
        if len(seqs) != len(set(seqs)):
            raise ViewInvariantError("待发布事务的提交序号存在重复")
        ordered = sorted(self._pending.items(), key=lambda kv: kv[1][0])
        self._pending.clear()
        pubs: list[Publication] = []
        for txn_id, (seq, bucket) in ordered:
            pubs.append(self.commit_txn(bucket, txn_id=txn_id, commit_seq=seq))
        return pubs

    def commit_txn(
        self,
        changes: list[Change],
        txn_id: Optional[str] = None,
        commit_seq: Optional[int] = None,
    ) -> Publication:
        """直接提交一个事务的全部变更，发布最终一致状态对应的净增量。"""
        for ch in changes:
            self._validate_change(ch)
        txn_id = txn_id if txn_id is not None else changes[0].txn_id
        commit_seq = (
            commit_seq if commit_seq is not None else changes[0].commit_seq
        )
        delta: Counter[TupleRow] = Counter()
        # 逐条作用到状态；同事务内的中间摆动（删后插、键迁移等）天然在
        # delta 这个多重集计数器里抵消，最终只剩净增量。
        for ch in changes:
            if ch.table == self.left_table:
                if ch.before is not None:
                    self._remove_left(ch.before, delta)
                if ch.after is not None:
                    self._add_left(ch.after, delta)
            else:
                if ch.before is not None:
                    self._remove_right(ch.before, delta)
                if ch.after is not None:
                    self._add_right(ch.after, delta)
        pub = self._publish(txn_id, commit_seq, delta)
        self.publications.append(pub)
        return pub

    def rows(self) -> list[TupleRow]:
        """当前视图的全部行（按重数展开，字典序排序）。"""
        out: list[TupleRow] = []
        for tup, n in sorted(self._multiset.items()):
            out.extend([tup] * n)
        return out

    def counts(self) -> dict[TupleRow, int]:
        return dict(self._multiset)

    # ------------------------------------------------------------------ #
    # 行构造
    # ------------------------------------------------------------------ #
    def _matched_tuple(self, left_row: Row, right_row: Row) -> TupleRow:
        merged = {f"l_{k}": v for k, v in left_row.items()}
        merged.update({f"r_{k}": v for k, v in right_row.items()})
        merged["matched"] = True
        return _sorted_tuple(merged)

    def _null_tuple(self, left_row: Row) -> TupleRow:
        merged = {f"l_{k}": v for k, v in left_row.items()}
        merged["matched"] = False
        return _sorted_tuple(merged)

    # ------------------------------------------------------------------ #
    # 左表维护
    # ------------------------------------------------------------------ #
    def _add_left(self, row: Row, delta: Counter) -> None:
        pk, key = row["pk"], row[self.join_key]
        if pk in self._left:
            raise ViewInvariantError(f"左表主键已存在: {pk!r}")
        self._left[pk] = row
        self._left_by_key.setdefault(key, Counter())[pk] += 1
        rights = self._right_by_key.get(key)
        if rights:
            for rpk in rights:
                delta[self._matched_tuple(row, self._right[rpk])] += 1
        else:
            delta[self._null_tuple(row)] += 1

    def _remove_left(self, row: Row, delta: Counter) -> None:
        pk, key = row["pk"], row[self.join_key]
        current = self._left.get(pk)
        if current is None:
            raise ViewInvariantError(f"左表主键不存在，无法删除: {pk!r}")
        if current != row:
            raise ViewInvariantError(
                f"左表前值与当前状态不一致: {pk!r}\n  before={row}\n  current={current}"
            )
        del self._left[pk]
        group = self._left_by_key[key]
        group[pk] -= 1
        if group[pk] == 0:
            del group[pk]
        if not group:
            del self._left_by_key[key]
        rights = self._right_by_key.get(key)
        if rights:
            for rpk in rights:
                delta[self._matched_tuple(row, self._right[rpk])] -= 1
        else:
            delta[self._null_tuple(row)] -= 1

    # ------------------------------------------------------------------ #
    # 右表维护
    # ------------------------------------------------------------------ #
    def _add_right(self, row: Row, delta: Counter) -> None:
        pk, key = row["pk"], row[self.join_key]
        if pk in self._right:
            raise ViewInvariantError(f"右表主键已存在: {pk!r}")
        had_match = key in self._right_by_key
        self._right[pk] = row
        self._right_by_key.setdefault(key, Counter())[pk] += 1
        lefts = self._left_by_key.get(key)
        if lefts:
            if not had_match:
                # 首个匹配到来：撤销每个左行的 NULL 扩展行
                for lpk in lefts:
                    delta[self._null_tuple(self._left[lpk])] -= 1
            for lpk in lefts:
                delta[self._matched_tuple(self._left[lpk], row)] += 1

    def _remove_right(self, row: Row, delta: Counter) -> None:
        pk, key = row["pk"], row[self.join_key]
        current = self._right.get(pk)
        if current is None:
            raise ViewInvariantError(f"右表主键不存在，无法删除: {pk!r}")
        if current != row:
            raise ViewInvariantError(
                f"右表前值与当前状态不一致: {pk!r}\n  before={row}\n  current={current}"
            )
        del self._right[pk]
        group = self._right_by_key[key]
        group[pk] -= 1
        if group[pk] == 0:
            del group[pk]
        last_match = not group
        if last_match:
            del self._right_by_key[key]
        lefts = self._left_by_key.get(key)
        if lefts:
            for lpk in lefts:
                delta[self._matched_tuple(self._left[lpk], row)] -= 1
            if last_match:
                # 最后一个匹配删除：恢复每个左行的 NULL 扩展行
                for lpk in lefts:
                    delta[self._null_tuple(self._left[lpk])] += 1

    # ------------------------------------------------------------------ #
    # 发布
    # ------------------------------------------------------------------ #
    def _publish(
        self, txn_id: str, commit_seq: int, delta: Counter
    ) -> Publication:
        inserted: list[TupleRow] = []
        deleted: list[TupleRow] = []
        for tup, n in sorted(delta.items()):
            if n == 0:
                continue
            if self._multiset[tup] + n < 0:
                raise ViewInvariantError(
                    f"视图行重数将变为负数: {dict(tup)} {self._multiset[tup]}+{n}"
                )
            self._multiset[tup] += n
            if self._multiset[tup] == 0:
                del self._multiset[tup]
            (inserted if n > 0 else deleted).extend([tup] * abs(n))
        return Publication(
            txn_id=txn_id,
            commit_seq=commit_seq,
            inserted=tuple(inserted),
            deleted=tuple(deleted),
        )

    # ------------------------------------------------------------------ #
    def _validate_change(self, ch: Change) -> None:
        if ch.table not in (self.left_table, self.right_table):
            raise ViewInvariantError(f"未知表: {ch.table!r}")
        if ch.before is None and ch.after is None:
            raise ViewInvariantError("before/after 不能同时为 None")
        for label, row in (("before", ch.before), ("after", ch.after)):
            if row is None:
                continue
            if "pk" not in row:
                raise ViewInvariantError(f"{label} 行缺少主键字段 'pk'")
            if self.join_key not in row:
                raise ViewInvariantError(
                    f"{label} 行缺少连接键字段 {self.join_key!r}"
                )
            if row["pk"] != ch.pk:
                raise ViewInvariantError(
                    f"{label} 行主键 {row['pk']!r} 与 Change.pk {ch.pk!r} 不一致"
                )
