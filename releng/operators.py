"""迭代器（火山）模型算子。每个算子的 execute(ctx) 是生成器，逐行产出 dict 行。

记账约定（所有算子一致，重复执行的子计划共享同一 Budget，不会漏记）：
- 每从输入（表或子算子）消费一行：charge_row(1) + charge_step(1)。
- 连接算子每做一次探测：charge_step(1)。
- 需要物化缓冲的算子（HashJoin 构建侧、HashAggregate 分组表、Sort 缓冲区）
  对每行/每组显式 mem_alloc，并在结束时（含异常路径）mem_free。

NULL 语义：比较走三值逻辑（见 core.sql_*），Filter 只保留谓词为 True 的行；
等值连接中 NULL 键永不匹配（NULL != NULL）；聚合忽略 NULL 输入；
GROUP BY 把 NULL 键归入同一组。

多重集合（bag）语义：重复行在 Scan/Filter/Project 中原样保留；
连接产生 |左匹配| x |右匹配| 行；count(*) 统计全部行含重复。
"""
from __future__ import annotations

import heapq
import pickle
from abc import ABC, abstractmethod

from .core import (
    BudgetExhausted,
    ExecutionContext,
    QueryCancelled,
    TerminationReason,
    estimate_row_size,
)


def merge_rows(left: dict, right: dict) -> dict:
    """连接输出行合并；列名冲突时右表列加 ``right.`` 前缀。"""
    out = dict(left)
    for key, value in right.items():
        if key in out:
            out[f"right.{key}"] = value
        else:
            out[key] = value
    return out


def _nulls_first_key(value):
    """排序键包装：NULL 升序时排最前（降序时随 reverse 排最后）。"""
    if value is None:
        return (0,)
    return (1, value)


class Operator(ABC):
    @abstractmethod
    def execute(self, ctx: ExecutionContext):
        """生成器：逐行产出 dict。子类必须在 finally 中关闭子迭代器并释放内存。"""


class Scan(Operator):
    """全表扫描（本地合成表，不调用任何 SQL 引擎）。"""

    def __init__(self, table):
        self.table = table

    def execute(self, ctx: ExecutionContext):
        for tup in self.table.rows:
            ctx.budget.charge_row()
            ctx.budget.charge_step()
            yield dict(zip(self.table.columns, tup))


class Filter(Operator):
    """谓词过滤。谓词返回 True 才保留；False / None（UNKNOWN）都过滤掉。"""

    def __init__(self, child: Operator, predicate):
        self.child = child
        self.predicate = predicate

    def execute(self, ctx: ExecutionContext):
        it = self.child.execute(ctx)
        try:
            for row in it:
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                if self.predicate(row) is True:
                    yield row
        finally:
            it.close()


class Project(Operator):
    """列投影，可重命名。columns: [(源列, 输出列), ...]"""

    def __init__(self, child: Operator, columns):
        self.child = child
        self.columns = list(columns)

    def execute(self, ctx: ExecutionContext):
        it = self.child.execute(ctx)
        try:
            for row in it:
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                yield {out: row.get(src) for src, out in self.columns}
        finally:
            it.close()


class Limit(Operator):
    """LIMIT：取够 n 行后探测输入是否还有剩余。

    有剩余 -> termination=LIMIT（正常提前结束，complete=True）；
    输入恰好耗尽 -> 保持 EXHAUSTED。以此区分 LIMIT 提前结束与预算中断。
    """

    def __init__(self, child: Operator, n: int):
        self.child = child
        self.n = n

    def execute(self, ctx: ExecutionContext):
        it = self.child.execute(ctx)
        try:
            count = 0
            while count < self.n:
                try:
                    row = next(it)
                except StopIteration:
                    return
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                yield row
                count += 1
            try:
                next(it)
            except StopIteration:
                return
            ctx.termination = TerminationReason.LIMIT
        finally:
            it.close()


class HashJoin(Operator):
    """等值哈希内连接：左表构建哈希表（占内存预算），右表流式探测。

    NULL 键永不匹配（三值逻辑）；重复键产生多重集合叉积。
    """

    def __init__(self, left: Operator, right: Operator, left_on: str, right_on: str):
        self.left = left
        self.right = right
        self.left_on = left_on
        self.right_on = right_on

    def execute(self, ctx: ExecutionContext):
        table: dict = {}
        held = 0
        lit = self.left.execute(ctx)
        try:
            for row in lit:
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                key = row.get(self.left_on)
                if key is None:
                    continue
                size = estimate_row_size(row)
                ctx.budget.mem_alloc(size)
                held += size
                table.setdefault(key, []).append(row)
        finally:
            lit.close()
        rit = self.right.execute(ctx)
        try:
            for rrow in rit:
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                ctx.budget.charge_step()  # 探测计一步
                key = rrow.get(self.right_on)
                if key is None:
                    continue
                for lrow in table.get(key, ()):
                    yield merge_rows(lrow, rrow)
        finally:
            rit.close()
            if held:
                ctx.budget.mem_free(held)


class NestedLoopJoin(Operator):
    """嵌套循环内连接：内层子计划对每一行外层行重新执行一次。

    内层的每次重复执行都向同一个 Budget 记账（行数与步骤），
    用于验证“重复子计划共享预算不漏记”。
    cond(lrow, rrow) 返回 True/False/None，仅 True 连接。
    """

    def __init__(self, left: Operator, right: Operator, cond):
        self.left = left
        self.right = right
        self.cond = cond

    def execute(self, ctx: ExecutionContext):
        lit = self.left.execute(ctx)
        try:
            for lrow in lit:
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                rit = self.right.execute(ctx)
                try:
                    for rrow in rit:
                        ctx.budget.charge_row()
                        ctx.budget.charge_step()
                        if self.cond(lrow, rrow) is True:
                            yield merge_rows(lrow, rrow)
                finally:
                    rit.close()
        finally:
            lit.close()


_AGG_FUNCS = ("count_star", "count", "sum", "avg", "min", "max")


class HashAggregate(Operator):
    """哈希分组聚合。aggregates: [(func, column|None, 输出别名), ...]

    func ∈ {count_star, count, sum, avg, min, max}；除 count_star 外均忽略 NULL；
    全 NULL 组的 sum/avg/min/max 为 None，count 为 0。

    关键性质：输入未消费完就被预算/取消打断时，输出当前累加器状态，
    并置 ctx.partial_aggregates=True —— 部分聚合绝不冒充最终值。
    """

    def __init__(self, child: Operator, group_by, aggregates):
        self.child = child
        self.group_by = list(group_by)
        self.aggregates = list(aggregates)
        for func, _, _ in self.aggregates:
            if func not in _AGG_FUNCS:
                raise ValueError(f"未知聚合函数: {func}")

    @staticmethod
    def _new_acc():
        return {"n": 0, "s": 0, "m": None}

    @staticmethod
    def _update(func, acc, value):
        if func == "count_star":
            acc["n"] += 1
        elif value is None:
            pass  # 聚合忽略 NULL
        elif func == "count":
            acc["n"] += 1
        elif func in ("sum", "avg"):
            acc["n"] += 1
            acc["s"] += value
        elif func == "min":
            acc["n"] += 1
            if acc["m"] is None or value < acc["m"]:
                acc["m"] = value
        elif func == "max":
            acc["n"] += 1
            if acc["m"] is None or value > acc["m"]:
                acc["m"] = value

    @staticmethod
    def _finalize(func, acc):
        if func in ("count_star", "count"):
            return acc["n"]
        if func == "sum":
            return acc["s"] if acc["n"] else None
        if func == "avg":
            return acc["s"] / acc["n"] if acc["n"] else None
        return acc["m"]  # min / max

    def execute(self, ctx: ExecutionContext):
        groups: dict = {}
        held = 0
        it = self.child.execute(ctx)
        try:
            try:
                for row in it:
                    ctx.budget.charge_row()
                    ctx.budget.charge_step()
                    key = tuple(row.get(c) for c in self.group_by)
                    accs = groups.get(key)
                    if accs is None:
                        size = 64 + 32 * len(self.aggregates) + 16 * len(key)
                        ctx.budget.mem_alloc(size)
                        held += size
                        accs = groups[key] = [self._new_acc() for _ in self.aggregates]
                    for (func, col, _), acc in zip(self.aggregates, accs):
                        self._update(func, acc, row.get(col) if col else None)
            except BudgetExhausted as exc:
                ctx.termination = exc.reason
                ctx.partial_aggregates = True
            except QueryCancelled:
                ctx.termination = TerminationReason.CANCELLED
                ctx.partial_aggregates = True
        finally:
            it.close()
            if held:
                ctx.budget.mem_free(held)
        for key, accs in groups.items():
            out = dict(zip(self.group_by, key))
            for (func, _, alias), acc in zip(self.aggregates, accs):
                out[alias] = self._finalize(func, acc)
            yield out


class Sort(Operator):
    """外排序：缓冲区达到 spill_bytes 即排序落盘为临时 run，最后归并。

    临时文件登记在 ResourceRegistry，无论正常结束、预算中断还是取消，
    执行器结束时统一释放；缓冲区内存也在 finally 中归还预算。
    """

    def __init__(self, child: Operator, key, reverse: bool = False, spill_bytes: int = 65536):
        self.child = child
        self.key = key
        self.reverse = reverse
        self.spill_bytes = spill_bytes

    def _keyfunc(self):
        if callable(self.key):
            return self.key
        return lambda row: _nulls_first_key(row.get(self.key))

    def _spill(self, ctx, buffer, keyfunc):
        buffer.sort(key=lambda item: keyfunc(item[1]), reverse=self.reverse)
        path = ctx.registry.new_temp_file()
        with open(path, "wb") as fh:
            for _, row in buffer:
                pickle.dump(row, fh)
        return path

    @staticmethod
    def _read_run(path):
        with open(path, "rb") as fh:
            while True:
                try:
                    yield pickle.load(fh)
                except EOFError:
                    return

    def execute(self, ctx: ExecutionContext):
        keyfunc = self._keyfunc()
        buffer: list = []
        buf_bytes = 0
        runs: list[str] = []
        it = self.child.execute(ctx)
        try:
            for row in it:
                ctx.budget.charge_row()
                ctx.budget.charge_step()
                size = estimate_row_size(row)
                ctx.budget.mem_alloc(size)
                buffer.append((size, row))
                buf_bytes += size
                if buf_bytes >= self.spill_bytes:
                    runs.append(self._spill(ctx, buffer, keyfunc))
                    for sz, _ in buffer:
                        ctx.budget.mem_free(sz)
                    buffer.clear()
                    buf_bytes = 0
            buffer.sort(key=lambda item: keyfunc(item[1]), reverse=self.reverse)
            streams = [iter([row for _, row in buffer])]
            streams.extend(self._read_run(p) for p in runs)
            for row in heapq.merge(*streams, key=keyfunc, reverse=self.reverse):
                yield row
        finally:
            it.close()
            for sz, _ in buffer:
                ctx.budget.mem_free(sz)
            buffer.clear()
