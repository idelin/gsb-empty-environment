"""延迟展开节点与预算求值。

动机：多项式乘法可能引起项数膨胀。本模块把表达式表示为惰性节点
（Leaf/Add/Mul/Deriv），求值由 :class:`Budget` 按"配额"切片推进：
预算耗尽时返回 :class:`Partial`（已累积部分 + 可继续的惰性表达式），
而不是截断的多项式；调用方可凭 :func:`resume` 继续，最终结果与一次性
求值完全相同（canonical 形式一致）。

适用域说明：所有节点变换仅用到加法、乘法与形式偏导的分配律，
对任意特征 0 交换环成立（本实现固定为 Q）。
"""
from __future__ import annotations

from dataclasses import dataclass

from poly import Polynomial, TermLimitExceeded

__all__ = [
    "Budget",
    "Lazy",
    "Leaf",
    "Add",
    "Mul",
    "Deriv",
    "Partial",
    "leaf",
    "evaluate",
    "resume",
    "evaluate_full",
    "TermLimitExceeded",
]


class Budget:
    """求值预算。

    * ``quota``：本切片允许的单项写入次数（>= 1 即可保证持续推进）。
    * ``max_terms``：项数膨胀硬上限，任一累加器超过即抛
      :class:`TermLimitExceeded`（这是"限制项数膨胀"的闸门）。
    * ``peak``：本切片观察到的峰值项数（供调用方记录）。
    """

    __slots__ = ("quota", "remaining", "max_terms", "peak")

    def __init__(self, quota: int, max_terms: int | None = None):
        if quota < 0:
            raise ValueError("quota 必须 >= 0")
        if max_terms is not None and max_terms < 0:
            raise ValueError("max_terms 必须 >= 0 或 None")
        self.quota = quota
        self.remaining = quota
        self.max_terms = max_terms
        self.peak = 0

    def observe(self, size: int) -> None:
        """记录累加器规模；超过硬上限立即失败。适用域：纯记账，无代数假设。"""
        if self.max_terms is not None and size > self.max_terms:
            raise TermLimitExceeded(
                f"项数 {size} 超过膨胀上限 max_terms={self.max_terms}"
            )
        if size > self.peak:
            self.peak = size

    def spend(self, acc_size: int) -> bool:
        """尝试为一次单项写入支付配额；配额耗尽返回 False（应当暂停）。"""
        self.observe(acc_size)
        if self.remaining <= 0:
            return False
        self.remaining -= 1
        return True


# ----------------------------------------------------------------- 节点
class Lazy:
    """惰性表达式节点基类。适用域：任意特征 0 交换环。"""

    def __add__(self, other: "Lazy") -> "Lazy":
        return Add(self, other)

    def __mul__(self, other: "Lazy") -> "Lazy":
        return Mul(self, other)

    def deriv(self, var: int) -> "Lazy":
        return Deriv(self, var)


@dataclass(frozen=True)
class Leaf(Lazy):
    """已物化的多项式叶子。"""

    poly: Polynomial


@dataclass(frozen=True)
class Add(Lazy):
    """加法节点。适用域：任意交换环（加法交换律、结合律）。"""

    left: Lazy
    right: Lazy


@dataclass(frozen=True)
class Mul(Lazy):
    """乘法节点。适用域：任意交换环（分配律）。"""

    left: Lazy
    right: Lazy


@dataclass(frozen=True)
class Deriv(Lazy):
    """偏导节点。适用域：特征 0 整环（形式求导的线性性与 Leibniz 律）。"""

    child: Lazy
    var: int


@dataclass
class Partial:
    """求值中间态：``acc`` 已累积的多项式部分，``rest`` 可继续的惰性表达式。

    不变式：最终数学结果 == acc + evaluate(rest)。这不是截断——
    剩余部分完整保留在 rest 中，凭 :func:`resume` 可继续推进。
    """

    acc: Polynomial
    rest: Lazy


def leaf(poly: Polynomial) -> Leaf:
    return Leaf(poly)


# ----------------------------------------------------------------- 求值
def evaluate(node: Lazy, budget: Budget):
    """以预算切片求值；返回 Polynomial（完成）或 Partial（可继续）。

    适用域：任意特征 0 交换环；所有改写均为分配律/线性性的恒等变换。
    """
    if isinstance(node, Leaf):
        budget.observe(node.poly.term_count)
        return node.poly
    if isinstance(node, Add):
        return _eval_add(node.left, node.right, budget)
    if isinstance(node, Mul):
        return _eval_mul(node.left, node.right, budget)
    if isinstance(node, Deriv):
        return _eval_deriv(node.child, node.var, budget)
    raise TypeError(f"未知惰性节点: {node!r}")


def resume(part: Partial, budget: Budget):
    """继续一个 Partial：等价于求值 ``acc + rest``。适用域：加法恒等式。"""
    return evaluate(Add(Leaf(part.acc), part.rest), budget)


def evaluate_full(node: Lazy, quota: int, max_terms: int | None = None):
    """以固定配额反复切片求值直至完成。

    返回 ``(polynomial, peak_terms, slices)``：canonical 结果、全程峰值
    项数、使用的切片数。quota >= 1 时保证终止。
    """
    if quota < 1:
        raise ValueError("evaluate_full 需要 quota >= 1 以保证推进")
    budget = Budget(quota, max_terms)
    result = evaluate(node, budget)
    peak = budget.peak
    slices = 1
    while isinstance(result, Partial):
        budget = Budget(quota, max_terms)
        result = resume(result, budget)
        peak = max(peak, budget.peak)
        slices += 1
    return result, peak, slices


# ----------------------------------------------------------------- 内部
def _add_budgeted(p: Polynomial, q: Polynomial, budget: Budget):
    """预算内合并两个多项式；配额耗尽返回 Partial(acc, Leaf(剩余项))。"""
    acc = dict(p._terms)
    budget.observe(len(acc))
    items = list(q._terms.items())
    for i, (exp, coef) in enumerate(items):
        if not budget.spend(len(acc)):
            rest = Polynomial(p.nvars, items[i:])
            return Partial(Polynomial(p.nvars, acc), Leaf(rest))
        merged = acc.get(exp)
        merged = coef if merged is None else merged + coef
        if merged:
            acc[exp] = merged
        else:
            acc.pop(exp, None)
    budget.observe(len(acc))
    return Polynomial(p.nvars, acc)


def _eval_add(left: Lazy, right: Lazy, budget: Budget):
    pl = evaluate(left, budget)
    if isinstance(pl, Partial):
        # (acc_l + rest_l) + right == acc_l + (rest_l + right)
        return Partial(pl.acc, Add(pl.rest, right))
    pr = evaluate(right, budget)
    if isinstance(pr, Partial):
        # pl + (acc_r + rest_r) == (pl + acc_r) + rest_r
        merged = _add_budgeted(pl, pr.acc, budget)
        if isinstance(merged, Partial):
            return Partial(merged.acc, Add(merged.rest, pr.rest))
        return Partial(merged, pr.rest)
    return _add_budgeted(pl, pr, budget)


def _mul_budgeted(p: Polynomial, q: Polynomial, budget: Budget):
    """逐行（p 的每个单项）展开 p*q；配额耗尽时把未算部分打包成惰性节点。"""
    p_items = list(p._terms.items())
    q_items = list(q._terms.items())
    acc: dict = {}
    budget.observe(0)
    for i, (pe, pc) in enumerate(p_items):
        for j, (qe, qc) in enumerate(q_items):
            if not budget.spend(len(acc)):
                rest = _mul_rest(p_items, q_items, i, j, p.nvars)
                return Partial(Polynomial(p.nvars, acc), rest)
            exp = tuple(a + b for a, b in zip(pe, qe))
            merged = acc.get(exp)
            merged = pc * qc if merged is None else merged + pc * qc
            if merged:
                acc[exp] = merged
            else:
                acc.pop(exp, None)
    budget.observe(len(acc))
    return Polynomial(p.nvars, acc)


def _mul_rest(p_items, q_items, i: int, j: int, nvars: int) -> Lazy:
    """把 p_items[i:]*q_items 中未展开的部分表示为惰性表达式。

    拆分恒等式（适用域：分配律）：
        rest == p_i * q[j:]  +  p[i+1:] * q
    """
    row = Polynomial(nvars, [p_items[i]])
    q_rem = Polynomial(nvars, q_items[j:])
    head: Lazy = Mul(Leaf(row), Leaf(q_rem))
    if i + 1 < len(p_items):
        tail = Mul(
            Leaf(Polynomial(nvars, p_items[i + 1:])),
            Leaf(Polynomial(nvars, q_items)),
        )
        return Add(head, tail)
    return head


def _eval_mul(left: Lazy, right: Lazy, budget: Budget):
    pl = evaluate(left, budget)
    if isinstance(pl, Partial):
        # (acc_l + rest_l) * right == acc_l*right + rest_l*right
        zero = Polynomial.zero(pl.acc.nvars)
        return Partial(
            zero,
            Add(Mul(Leaf(pl.acc), right), Mul(pl.rest, right)),
        )
    pr = evaluate(right, budget)
    if isinstance(pr, Partial):
        # pl * (acc_r + rest_r) == pl*acc_r + pl*rest_r
        zero = Polynomial.zero(pl.nvars)
        return Partial(
            zero,
            Add(Mul(Leaf(pl), Leaf(pr.acc)), Mul(Leaf(pl), pr.rest)),
        )
    return _mul_budgeted(pl, pr, budget)


def _eval_deriv(child: Lazy, var: int, budget: Budget):
    pc = evaluate(child, budget)
    if isinstance(pc, Partial):
        # d(acc + rest) == d(acc) + d(rest)（求导线性性，特征 0）
        zero = Polynomial.zero(pc.acc.nvars)
        return Partial(
            zero,
            Add(Deriv(Leaf(pc.acc), var), Deriv(pc.rest, var)),
        )
    return pc.deriv(var)
