"""事务化引擎:维护 A、B、M 的增量掩码乘积,发布按版本向量绑定的快照。"""

import threading

from .errors import (
    BoundsError,
    RetractionError,
    SpillError,
    TransactionStateError,
    UnknownVersionError,
)
from .index import ContributionIndex, SpillStore
from .state import SparseMatrix


class Engine:
    """A(rows x inner) 乘 B(inner x cols) 在掩码 M 下的增量结果。

    - 每个事务可批量携带 A/B/M 的增删操作,提交时发布不可变快照。
    - 查询必须绑定完整版本向量 (vA, vB, vM),未提交过的向量被拒绝,
      因此不存在跨事务拼接矩阵的入口。
    - 撤销(abort)与溢写失败都会按日志回滚,已发布快照不受影响。
    """

    def __init__(self, rows, inner, cols, mask, row_threshold=1024, spill_store=None):
        if mask.rows != rows or mask.cols != cols:
            raise ValueError("掩码尺寸与结果尺寸不一致")
        self.rows = rows
        self.inner = inner
        self.cols = cols
        self.A = SparseMatrix(rows, inner)
        self.B = SparseMatrix(inner, cols)
        self.mask = mask
        self.spill_store = spill_store or SpillStore()
        self.index = ContributionIndex(self.spill_store, row_threshold)
        self.versions = [0, 0, 0]
        self.published = {}
        self._lock = threading.Lock()
        self._txn = None
        self.latest = tuple(self.versions)
        self.published[self.latest] = self.index.materialize()

    def begin(self):
        with self._lock:
            if self._txn is not None:
                raise TransactionStateError("已有进行中的事务")
            txn = Transaction(self)
            self._txn = txn
            return txn

    def _end(self, txn):
        with self._lock:
            if self._txn is txn:
                self._txn = None

    def query(self, version_vector):
        """按版本向量取快照;向量必须来自某次真实提交。"""
        key = tuple(version_vector)
        try:
            return self.published[key]
        except KeyError:
            raise UnknownVersionError(f"版本向量 {key} 从未发布") from None

    def query_latest(self):
        return self.published[self.latest]

    def _commit(self, txn):
        for component in txn._touched:
            self.versions[component] += 1
        try:
            snapshot = self.index.materialize()
        except Exception:
            for component in txn._touched:
                self.versions[component] -= 1
            raise
        vec = tuple(self.versions)
        self.published[vec] = snapshot
        self.latest = vec

    def _a_delta(self, i, k, w, sign):
        self.A.apply_delta(i, k, sign * w, sign)
        for j, (bw, bc) in list(self.B.row(k).items()):
            if self.mask.contains(i, j) or self.index.is_hidden(i, j):
                self.index.add_source(i, j, k, sign * w * bw, sign * bc)

    def _b_delta(self, k, j, w, sign):
        self.B.apply_delta(k, j, sign * w, sign)
        for i, (aw, ac) in list(self.A.col(k).items()):
            if self.mask.contains(i, j) or self.index.is_hidden(i, j):
                self.index.add_source(i, j, k, sign * aw * w, sign * ac)

    def _m_add(self, i, j):
        was = self.mask.contains(i, j)
        self.mask.add(i, j)
        now = self.mask.contains(i, j)
        if was or not now:
            return
        if self.index.unhide_cell(i, j):
            return
        for k, (aw, ac) in self.A.row(i).items():
            bw = self.B.weight(k, j)
            bc = self.B.count(k, j)
            if bw != 0:
                self.index.add_source(i, j, k, aw * bw, ac * bc)

    def _m_remove(self, i, j):
        was = self.mask.contains(i, j)
        self.mask.remove(i, j)
        now = self.mask.contains(i, j)
        if was and not now:
            self.index.hide_cell(i, j)

    def _apply_primitive(self, op):
        kind = op[0]
        if kind == "a":
            _k, i, k, w, sign = op
            self._a_delta(i, k, w, sign)
        elif kind == "b":
            _k, k, j, w, sign = op
            self._b_delta(k, j, w, sign)
        elif kind == "madd":
            self._m_add(op[1], op[2])
        elif kind == "mdel":
            self._m_remove(op[1], op[2])
        else:
            raise ValueError(f"未知原语: {op!r}")


class Transaction:
    """批量事务:操作即时生效并记录逆操作,提交或回滚二选一。"""

    def __init__(self, engine):
        self._engine = engine
        self._journal = []
        self._touched = set()
        self._state = "open"

    @property
    def state(self):
        return self._state

    def _op(self, op, inverse, component):
        if self._state != "open":
            raise TransactionStateError("事务已结束")
        self._journal.append(inverse)
        try:
            self._engine._apply_primitive(op)
        except (RetractionError, BoundsError):
            self._journal.pop()
            raise
        except Exception:
            self.abort()
            raise
        self._touched.add(component)

    def a_insert(self, i, k, w=1):
        self._op(("a", i, k, w, 1), ("a", i, k, w, -1), 0)

    def a_delete(self, i, k, w=1):
        self._op(("a", i, k, w, -1), ("a", i, k, w, 1), 0)

    def b_insert(self, k, j, w=1):
        self._op(("b", k, j, w, 1), ("b", k, j, w, -1), 1)

    def b_delete(self, k, j, w=1):
        self._op(("b", k, j, w, -1), ("b", k, j, w, 1), 1)

    def m_insert(self, i, j):
        self._op(("madd", i, j), ("mdel", i, j), 2)

    def m_delete(self, i, j):
        self._op(("mdel", i, j), ("madd", i, j), 2)

    def commit(self):
        if self._state != "open":
            raise TransactionStateError("事务已结束")
        engine = self._engine
        try:
            engine._commit(self)
        except Exception:
            self._rollback()
            self._state = "aborted"
            engine._end(self)
            raise
        self._state = "committed"
        engine._end(self)

    def abort(self):
        if self._state != "open":
            raise TransactionStateError("事务已结束")
        self._rollback()
        self._state = "aborted"
        self._engine._end(self)

    def _rollback(self):
        for inverse in reversed(self._journal):
            try:
                self._engine._apply_primitive(inverse)
            except SpillError:
                pass

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        if self._state == "open":
            if exc_type is None:
                self.commit()
            else:
                self.abort()
        return False
