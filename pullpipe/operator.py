"""Pull 式算子。

* 每个算子实现 ``_next()``：返回非空 Batch、空 Batch（合法心跳）或 None（EOF）。
* 取消在批次边界检查（聚合构建在组循环内也检查），取消后抛 CancelledError。
* 算子内部异常包装为 OperatorError；根算子 close 按「先子后己」递归释放，
  因此下游出错 / 被取消 / limit 提前结束时，上游所有资源都会被释放。
"""

from __future__ import annotations

from dataclasses import dataclass
from functools import cmp_to_key
from typing import Callable, Iterator, Sequence

from .batch import Batch, EOF, Row
from .errors import CancelledError, OperatorError
from .expr import Expr, bind_all
from .null import Null, is_null, is_true
from .table import Schema, Table


class CancellationToken:
    __slots__ = ("_cancelled", "reason")

    def __init__(self):
        self._cancelled = False
        self.reason: str | None = None

    def cancel(self, reason: str = "cancelled") -> None:
        if not self._cancelled:
            self._cancelled = True
            self.reason = reason

    @property
    def cancelled(self) -> bool:
        return self._cancelled

    def raise_if_cancelled(self) -> None:
        if self._cancelled:
            raise CancelledError(self.reason or "pipeline cancelled")


class Operator:
    #: 该算子（含子树）读入的批次数，便于测试与观测。
    child: "Operator | None" = None

    def __init__(self):
        self.token: CancellationToken | None = None
        self._closed = False
        self.release_count = 0
        self.batches_in = 0

    # --- 结构 ----------------------------------------------------------
    @property
    def columns(self) -> Schema:
        raise NotImplementedError

    def children(self) -> tuple["Operator", ...]:
        return (self.child,) if self.child is not None else ()

    def bind_token(self, token: CancellationToken) -> None:
        """把取消令牌沿算子树下发到每个算子。"""
        self.token = token
        for c in self.children():
            c.bind_token(token)

    # --- 拉取 ----------------------------------------------------------
    def _checkpoint(self) -> None:
        if self.token is not None:
            self.token.raise_if_cancelled()

    def next_batch(self) -> Batch | None:
        self._checkpoint()
        try:
            batch = self._next()
        except (CancelledError, OperatorError):
            raise
        except Exception as exc:  # 算子内部错误统一包装，保留原因
            raise OperatorError(type(self).__name__, exc) from exc
        return batch

    def _next(self) -> Batch | None:
        raise NotImplementedError

    # --- 关闭 ----------------------------------------------------------
    def close(self) -> None:
        """幂等关闭：先关闭所有子算子，再释放自身资源。"""
        if self._closed:
            return
        errors: list[BaseException] = []
        for c in self.children():
            try:
                c.close()
            except BaseException as exc:  # 继续关闭其余子节点，最后抛出
                errors.append(exc)
        try:
            self._release()
            self.release_count += 1
        except BaseException as exc:
            errors.append(exc)
        self._closed = True
        if errors:
            raise errors[0]

    @property
    def closed(self) -> bool:
        return self._closed

    def _release(self) -> None:
        """释放本算子自身资源（子算子由基类统一关闭）。默认无事可做。"""

    def _pull(self) -> Batch | None:
        batch = self.child.next_batch()
        if batch is not None:
            self.batches_in += 1
        return batch


# ---------------------------------------------------------------------
# 扫描
# ---------------------------------------------------------------------
class TableScan(Operator):
    def __init__(self, table: Table, batch_size: int = 1024):
        super().__init__()
        if batch_size <= 0:
            raise ValueError("batch_size must be positive")
        self.table = table
        self.batch_size = batch_size
        self._cursor = None
        self._eof = False

    @property
    def columns(self) -> Schema:
        return self.table.schema

    def _next(self) -> Batch | None:
        if self._eof:
            return EOF
        if self._cursor is None:
            self._cursor = self.table.open()
        rows = self._cursor.read_rows(self.batch_size)
        if not rows:
            self._eof = True
            self._release_cursor()
            return EOF
        return Batch(rows)

    def _release_cursor(self) -> None:
        if self._cursor is not None:
            self._cursor.close()

    def _release(self) -> None:
        self._release_cursor()


# ---------------------------------------------------------------------
# 过滤 / 投影 / 截断
# ---------------------------------------------------------------------
class Filter(Operator):
    def __init__(self, child: Operator, predicate: Expr):
        super().__init__()
        self.child = child
        self.predicate = predicate.bind(child.columns)

    @property
    def columns(self) -> Schema:
        return self.child.columns

    def _next(self) -> Batch | None:
        while True:
            batch = self._pull()
            if batch is None:
                return EOF
            kept = [r for r in batch.rows if is_true(self.predicate.eval(r))]
            if kept:
                return Batch(kept)
            # 本批全部被过滤：返回空 Batch，而不是继续拉到有数据为止——
            # 空批次必须能够沿管线原样传播。


class Project(Operator):
    def __init__(
        self,
        child: Operator,
        expressions: Sequence[Expr],
        names: Sequence[str] | None = None,
    ):
        super().__init__()
        self.child = child
        if names is not None and len(names) != len(expressions):
            raise ValueError("names length must match expressions")
        self._exprs = bind_all(expressions, child.columns)
        self._names = list(names) if names is not None else [f"col{i}" for i in range(len(expressions))]
        self._schema = Schema(self._names)

    @property
    def columns(self) -> Schema:
        return self._schema

    def _next(self) -> Batch | None:
        batch = self._pull()
        if batch is None:
            return EOF
        return Batch(tuple(e.eval(r) for e in self._exprs) for r in batch.rows)


class Limit(Operator):
    """LIMIT n：取满后立即关闭上游；n=0 首次拉取即触发整树关闭。"""

    def __init__(self, child: Operator, n: int):
        super().__init__()
        if n < 0:
            raise ValueError("limit must be >= 0")
        self.child = child
        self.n = n
        self._taken = 0
        self._eof = False

    @property
    def columns(self) -> Schema:
        return self.child.columns

    def _next(self) -> Batch | None:
        if self._eof:
            return EOF
        if self.n == 0:
            self._eof = True
            self.child.close()
            return EOF
        if self._taken >= self.n:
            self._eof = True
            self.child.close()
            return EOF
        batch = self._pull()
        if batch is None:
            self._eof = True
            return EOF
        remain = self.n - self._taken
        if len(batch) > remain:
            batch = Batch(batch.rows[:remain])
        self._taken += len(batch)
        if self._taken >= self.n:
            self._eof = True
            self.child.close()  # 提前停止：立刻释放上游全部资源
        return batch

    def _release(self) -> None:
        # next_batch 已 EOF 后若再被根 close 调用，child.close() 幂等。
        if not self.child.closed:
            self.child.close()


# ---------------------------------------------------------------------
# 内连接（hash）
# ---------------------------------------------------------------------
class HashJoin(Operator):
    """内等值连接。

    多重集合语义：左一行键命中右表 m 行就产出 m 行；两边重复键做笛卡尔展开。
    NULL 键永不连接（NULL = NULL 为 Unknown），与 SQL 一致。
    """

    def __init__(
        self,
        left: Operator,
        right: Operator,
        left_key: Expr,
        right_key: Expr,
        batch_size: int = 1024,
    ):
        super().__init__()
        if batch_size <= 0:
            raise ValueError("batch_size must be positive")
        self.batch_size = batch_size
        self._left = left
        self._right = right
        self._left_key = left_key.bind(left.columns)
        self._right_key = right_key.bind(right.columns)
        self._schema = Schema(list(left.columns.names) + list(right.columns.names))
        self._built = False
        self._index: dict = {}
        self._probe_gen: Iterator | None = None

    @property
    def child(self) -> "Operator | None":  # type: ignore[override]
        return self._left

    @property
    def columns(self) -> Schema:
        return self._schema

    def children(self) -> tuple[Operator, ...]:
        return (self._left, self._right)

    def _build(self) -> None:
        while True:
            self._checkpoint()
            batch = self._right.next_batch()
            if batch is None:
                break
            for row in batch.rows:
                key = self._right_key.eval(row)
                if not is_null(key):
                    self._index.setdefault(key, []).append(row)
        self._built = True

    def _probe(self) -> "Iterator[Row]":
        while True:
            self._checkpoint()
            batch = self._left.next_batch()
            if batch is None:
                return
            for left_row in batch.rows:
                key = self._left_key.eval(left_row)
                if is_null(key):
                    continue
                for right_row in self._index.get(key, ()):
                    yield left_row + right_row

    def _next(self) -> Batch | None:
        if not self._built:
            self._build()
            self._probe_gen = self._probe()
        rows: list[Row] = []
        for row in self._probe_gen:
            rows.append(row)
            if len(rows) >= self.batch_size:
                break
        if not rows:
            return EOF
        return Batch(rows)

    def _release(self) -> None:
        if self._probe_gen is not None:
            self._probe_gen.close()


# ---------------------------------------------------------------------
# 哈希聚合
# ---------------------------------------------------------------------
@dataclass(frozen=True, slots=True)
class AggDef:
    func: str          # COUNT / SUM / AVG / MIN / MAX / COUNT_ALL
    value: Expr | None = None
    name: str | None = None


class HashAggregate(Operator):
    """GROUP BY 聚合。

    * groups 为空键时表示全局聚合（无 GROUP BY），即使输入 0 行也输出一行。
    * NULL 是一个分组键：所有 NULL 归为同一组（SQL 分组语义）。
    * COUNT(*) 计行数；COUNT(expr) 忽略 NULL；SUM/AVG 忽略 NULL；
      全 NULL 时 COUNT=0、SUM=NULL、AVG=NULL、MIN/MAX=NULL。
    * 聚合必须消费完整输入才能输出，故在构建循环内逐批检查取消。
    """

    _FUNCS = ("COUNT", "COUNT_ALL", "SUM", "AVG", "MIN", "MAX")

    def __init__(
        self,
        child: Operator,
        aggs: Sequence[AggDef],
        group_keys: Sequence[Expr] = (),
        group_names: Sequence[str] | None = None,
    ):
        super().__init__()
        if not aggs:
            raise ValueError("at least one aggregate is required")
        for a in aggs:
            if a.func not in self._FUNCS:
                raise ValueError(f"unsupported aggregate {a.func!r}")
        self.child = child
        self._group_exprs = bind_all(group_keys, child.columns)
        self._agg_defs = list(aggs)
        self._agg_exprs = [
            bind_all([a.value], child.columns)[0] if a.value is not None else None
            for a in aggs
        ]
        if group_names is not None and len(group_names) != len(self._group_exprs):
            raise ValueError("group_names length mismatch")
        self._group_names = (
            list(group_names)
            if group_names is not None
            else [f"key{i}" for i in range(len(self._group_exprs))]
        )
        agg_names = [
            a.name
            or (a.func.lower() if a.func != "COUNT_ALL" else "count")
            for a in self._agg_defs
        ]
        self._schema = Schema(self._group_names + agg_names)
        # 每组状态：list，每个聚合一个可变 state
        self._groups: dict[tuple, list] = {}
        self._order: list[tuple] = []
        self._built = False
        self._emit_pos = 0

    @property
    def columns(self) -> Schema:
        return self._schema

    @staticmethod
    def _new_state(func: str) -> list:
        if func in ("COUNT", "COUNT_ALL"):
            return [0]
        if func in ("SUM", "AVG"):
            return [0, 0]          # sum, count
        return [Null]             # MIN / MAX

    def _accumulate(self, func: str, state: list, value) -> None:
        if func == "COUNT_ALL":
            state[0] += 1
            return
        if func == "COUNT":
            if not is_null(value):
                state[0] += 1
            return
        if is_null(value):
            return
        if func in ("SUM", "AVG"):
            state[0] += value
            state[1] += 1
            return
        cur = state[0]
        if cur is Null or (func == "MIN" and value < cur) or (func == "MAX" and value > cur):
            state[0] = value

    @staticmethod
    def _finalize(func: str, state: list):
        if func in ("COUNT", "COUNT_ALL"):
            return state[0]
        if func == "SUM":
            return state[0] if state[1] else Null
        if func == "AVG":
            return state[0] / state[1] if state[1] else Null
        return state[0]

    def _build(self) -> None:
        while True:
            self._checkpoint()
            batch = self.child.next_batch()
            if batch is None:
                break
            for row in batch.rows:
                key = tuple(e.eval(row) for e in self._group_exprs)
                states = self._groups.get(key)
                if states is None:
                    states = [self._new_state(a.func) for a in self._agg_defs]
                    self._groups[key] = states
                    self._order.append(key)
                for i, expr in enumerate(self._agg_exprs):
                    value = expr.eval(row) if expr is not None else None
                    self._accumulate(self._agg_defs[i].func, states[i], value)
        if not self._group_exprs and not self._groups:
            # 全局聚合：0 行输入也输出一行（COUNT=0，SUM/AVG/MIN/MAX=NULL）
            key: tuple = ()
            self._groups[key] = [self._new_state(a.func) for a in self._agg_defs]
            self._order.append(key)
        self._built = True

    def _next(self) -> Batch | None:
        if not self._built:
            self._build()
        if self._emit_pos >= len(self._order):
            return EOF
        rows: list[Row] = []
        while self._emit_pos < len(self._order) and len(rows) < 1024:
            key = self._order[self._emit_pos]
            self._emit_pos += 1
            states = self._groups[key]
            vals = [self._finalize(self._agg_defs[i].func, states[i]) for i in range(len(self._agg_defs))]
            rows.append(tuple(key) + tuple(vals))
        return Batch(rows)


# ---------------------------------------------------------------------
# 排序 / 并集
# ---------------------------------------------------------------------
class Sort(Operator):
    """全量排序（内存）。key 为表达式，desc 控制升降，NULL 在 ASC 最前、DESC 最后。"""

    def __init__(
        self,
        child: Operator,
        keys: Sequence[Expr],
        descs: Sequence[bool] | None = None,
    ):
        super().__init__()
        self.child = child
        if not keys:
            raise ValueError("Sort needs at least one key")
        self._keys = bind_all(keys, child.columns)
        self._descs = list(descs) if descs is not None else [False] * len(keys)
        if len(self._descs) != len(self._keys):
            raise ValueError("descs length must match keys")
        self._sorted: list[Row] = []
        self._pos = 0
        self._built = False

    @property
    def columns(self) -> Schema:
        return self.child.columns

    def _compare(self, a: Row, b: Row) -> int:
        for expr, desc in zip(self._keys, self._descs):
            va, vb = expr.eval(a), expr.eval(b)
            na, nb = is_null(va), is_null(vb)
            if na and nb:
                continue
            if na or nb:
                null_first = not desc
                if na:
                    return -1 if null_first else 1
                return 1 if null_first else -1
            if va < vb:
                return 1 if desc else -1
            if va > vb:
                return -1 if desc else 1
        return 0

    def _next(self) -> Batch | None:
        if not self._built:
            all_rows: list[Row] = []
            while True:
                self._checkpoint()
                batch = self.child.next_batch()
                if batch is None:
                    break
                all_rows.extend(batch.rows)
            all_rows.sort(key=cmp_to_key(self._compare))
            self._sorted = all_rows
            self._built = True
        if self._pos >= len(self._sorted):
            return EOF
        part = self._sorted[self._pos:self._pos + 1024]
        self._pos += len(part)
        return Batch(part)


class UnionAll(Operator):
    """UNION ALL：保留重复（多重集合语义）。先抽干左子树，再抽右子树。"""

    def __init__(self, left: Operator, right: Operator):
        super().__init__()
        self._left = left
        self._right = right
        if left.columns.names != right.columns.names:
            raise ValueError("UnionAll inputs must share the same schema")
        self._schema = left.columns
        self._side_left = True

    @property
    def child(self) -> "Operator | None":  # type: ignore[override]
        return self._left

    @property
    def columns(self) -> Schema:
        return self._schema

    def children(self) -> tuple[Operator, ...]:
        return (self._left, self._right)

    def _next(self) -> Batch | None:
        if self._side_left:
            batch = self._left.next_batch()
            if batch is not None:
                return batch
            self._side_left = False
        return self._right.next_batch()


# ---------------------------------------------------------------------
# 测试辅助算子
# ---------------------------------------------------------------------
class Nothing(Operator):
    """从一开始就是 EOF 的算子。"""

    def __init__(self, schema: Sequence[str] | Schema):
        super().__init__()
        self._schema = schema if isinstance(schema, Schema) else Schema(schema)

    @property
    def columns(self) -> Schema:
        return self._schema

    def _next(self) -> Batch | None:
        return EOF


class EmptyInjector(Operator):
    """先产出一个空 Batch，再透传下游所有批次（含 EOF）。

    用于证明空批次 != EOF：管线必须继续拉取并最终拿到真实数据。
    """

    def __init__(self, child: Operator):
        super().__init__()
        self.child = child
        self._injected = False

    @property
    def columns(self) -> Schema:
        return self.child.columns

    def _next(self) -> Batch | None:
        if not self._injected:
            self._injected = True
            return Batch()
        return self._pull()


class Exploder(Operator):
    """下游算子：拉第一批后，计算时必定抛错（用于错误关闭测试）。"""

    def __init__(self, child: Operator):
        super().__init__()
        self.child = child
        self._armed = True

    @property
    def columns(self) -> Schema:
        return self.child.columns

    def _next(self) -> Batch | None:
        batch = self._pull()
        if batch is None:
            return EOF
        raise RuntimeError("boom from downstream operator")
