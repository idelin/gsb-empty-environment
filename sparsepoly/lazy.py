"""惰性表达式：预算不足时返回可继续计算的表达式，而非截断多项式。

节点类型：Leaf（多项式叶）、Add、Mul、Deriv，以及乘法续算节点
``_MulCont``（记录行/列游标、内存累加器与已溢写运行段）。
每个节点的 ``evaluate(budget)`` 要么返回 Polynomial（完成），要么返回
新的 Expr（可继续）；用更大的预算再次求值即可恢复计算。

适用域：与 core 相同（Q 上交换环、特征 0）；所有中间合并均为精确
有理数运算，断点位置不影响最终规范输出。
"""

from __future__ import annotations

import os
from dataclasses import dataclass

from .budget import Budget, StepsExhausted
from .core import Polynomial
from .extmerge import merge_runs, spill_run


class Expr:
    """可继续计算的表达式节点。"""

    def evaluate(self, budget: Budget):
        raise NotImplementedError


def _eval_child(node, budget: Budget):
    return node.evaluate(budget) if isinstance(node, Expr) else node


@dataclass(frozen=True)
class Leaf(Expr):
    poly: Polynomial

    def evaluate(self, budget: Budget):
        return self.poly


@dataclass(frozen=True)
class Add(Expr):
    """加法节点。适用域：任意交换环（此处 Q）。"""

    left: object
    right: object

    def evaluate(self, budget: Budget):
        l = _eval_child(self.left, budget)
        if isinstance(l, Expr):
            return Add(l, self.right)
        r = _eval_child(self.right, budget)
        if isinstance(r, Expr):
            return Add(l, r)
        result = l + r
        budget.track_terms(result.num_terms)
        return result


@dataclass(frozen=True)
class Deriv(Expr):
    """偏导节点。适用域：特征 0（见 Polynomial.partial 的说明）。"""

    child: object
    var: int

    def evaluate(self, budget: Budget):
        c = _eval_child(self.child, budget)
        if isinstance(c, Expr):
            return Deriv(c, self.var)
        result = c.partial(self.var)
        budget.track_terms(result.num_terms)
        return result


@dataclass(frozen=True)
class Mul(Expr):
    """乘法节点。适用域：交换环；交叉项按指数和精确合并。"""

    left: object
    right: object

    def evaluate(self, budget: Budget):
        l = _eval_child(self.left, budget)
        if isinstance(l, Expr):
            return Mul(l, self.right)
        r = _eval_child(self.right, budget)
        if isinstance(r, Expr):
            return Mul(l, r)
        return _mul_budgeted(l.sorted_terms(), r.sorted_terms(), 0, 0, {},
                             [], l.nvars, budget)


class _MulCont(Expr):
    """乘法续算节点：断点续算状态，不是截断结果。

    rows/q_terms 为两个操作数的规范项表；(row, col) 为游标；acc 为内存
    累加器；runs 为已溢写的运行段文件路径。
    """

    def __init__(self, rows, q_terms, row, col, acc, runs, nvars):
        self.rows = rows
        self.q_terms = q_terms
        self.row = row
        self.col = col
        self.acc = acc
        self.runs = runs
        self.nvars = nvars

    def evaluate(self, budget: Budget):
        return _mul_budgeted(self.rows, self.q_terms, self.row, self.col,
                             self.acc, self.runs, self.nvars, budget)

    def discard(self) -> None:
        """放弃计算时清理已溢写的运行段文件。"""
        for p in self.runs:
            try:
                os.remove(p)
            except FileNotFoundError:
                pass
        self.runs.clear()

    def __repr__(self):
        return (f"_MulCont(row={self.row}/{len(self.rows)}, "
                f"acc_terms={len(self.acc)}, spilled_runs={len(self.runs)})")


def _mul_budgeted(rows, q_terms, row, col, acc, runs, nvars, budget: Budget):
    """带预算的稀疏乘法：逐行展开，支持溢写与断点续算。"""
    i, j = row, col
    while i < len(rows):
        pe, pc = rows[i]
        while j < len(q_terms):
            try:
                budget.account_step()
            except StepsExhausted:
                return _MulCont(rows, q_terms, i, j, acc, runs, nvars)
            qe, qc = q_terms[j]
            exp = tuple(a + b for a, b in zip(pe, qe))
            c = pc * qc
            old = acc.get(exp)
            if old is None:
                acc[exp] = c
            else:
                v = old + c
                if v == 0:
                    del acc[exp]  # 零项即时回收
                else:
                    acc[exp] = v
            j += 1
            budget.track_terms(len(acc))
            if len(acc) > budget.max_live_terms:
                if budget.allow_external:
                    runs.append(spill_run(acc, budget.spill_dir))
                    budget.spilled_runs += 1
                    acc.clear()
                else:
                    # 内存额度不足且不允许外存：返回可继续表达式
                    return _MulCont(rows, q_terms, i, j, acc, runs, nvars)
        i += 1
        j = 0
    if runs:
        return merge_runs(runs, acc, nvars, budget)
    return Polynomial(nvars, acc)


def evaluate_fully(node, budget: Budget) -> Polynomial:
    """反复求值直到得到 Polynomial。

    要求预算最终足够：不允许外存时 max_live_terms 必须容得下最大中间
    累加器，否则会在同一断点循环（这是调用方的预算配置问题，不是截断）。
    """
    while isinstance(node, Expr):
        node = node.evaluate(budget)
    return node
