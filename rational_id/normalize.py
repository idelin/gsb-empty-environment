"""把语法树规范化为 (分子, 分母, 定义域分母) 三元组。

- 分子/分母：未约分的多项式，整体表示有理函数 num/den。
- 定义域分母：表达式中所有出现过的分母（含除数的分子）之积；
  它在某点为 0 当且仅当原表达式在该点无定义（除数为零）。
每一步变换都向日志写入该步的适用域（变换成立的条件）。
"""
from __future__ import annotations

from .ast_nodes import Add, Const, Div, Mul, Neg, Pow, Sub, Var
from .poly import Poly, add, mul, neg, poly_pow, sub


class NormalForm:
    __slots__ = ("num", "den", "domain_den")

    def __init__(self, num, den, domain_den):
        self.num = num
        self.den = den
        self.domain_den = domain_den


def normalize(node, var_index, nvars, budget, log):
    if isinstance(node, Const):
        log.append(f"常量 {node.value}：以有理数精确表示；适用域：全体有理点")
        one = Poly.constant(nvars, 1)
        return NormalForm(Poly.constant(nvars, node.value), one, one)
    if isinstance(node, Var):
        log.append(f"变量 {node.name}：视为单项式；适用域：全体有理点")
        one = Poly.constant(nvars, 1)
        return NormalForm(Poly.variable(nvars, var_index[node.name]), one, one)
    if isinstance(node, Neg):
        inner = normalize(node.operand, var_index, nvars, budget, log)
        log.append("取负：-(n/d) = (-n)/d；适用域：与运算数相同")
        return NormalForm(neg(inner.num), inner.den, inner.domain_den)
    if isinstance(node, (Add, Sub)):
        left = normalize(node.left, var_index, nvars, budget, log)
        right = normalize(node.right, var_index, nvars, budget, log)
        right_num = mul(right.num, left.den, budget)
        if isinstance(node, Sub):
            right_num = neg(right_num)
        num = add(mul(left.num, right.den, budget), right_num, budget)
        den = mul(left.den, right.den, budget)
        dom = mul(left.domain_den, right.domain_den, budget)
        log.append("加减法通分：(n1/d1) ± (n2/d2) = (n1·d2 ± n2·d1)/(d1·d2)；"
                   "适用域：d1≠0 且 d2≠0（两侧均有定义）")
        return NormalForm(num, den, dom)
    if isinstance(node, Mul):
        left = normalize(node.left, var_index, nvars, budget, log)
        right = normalize(node.right, var_index, nvars, budget, log)
        log.append("乘法：(n1/d1)·(n2/d2) = (n1·n2)/(d1·d2)；"
                   "适用域：d1≠0 且 d2≠0（两侧均有定义）")
        return NormalForm(mul(left.num, right.num, budget),
                          mul(left.den, right.den, budget),
                          mul(left.domain_den, right.domain_den, budget))
    if isinstance(node, Div):
        left = normalize(node.left, var_index, nvars, budget, log)
        right = normalize(node.right, var_index, nvars, budget, log)
        log.append("除法：(n1/d1)/(n2/d2) = (n1·d2)/(d1·n2)；"
                   "适用域：d1≠0、d2≠0 且 n2≠0（除数非零）")
        return NormalForm(mul(left.num, right.den, budget),
                          mul(left.den, right.num, budget),
                          mul(mul(left.domain_den, right.domain_den, budget),
                              right.num, budget))
    if isinstance(node, Pow):
        base = normalize(node.base, var_index, nvars, budget, log)
        log.append(f"幂：A^{node.exp} 展开为重复乘法（平方连乘）；"
                   "适用域：底数 A 有定义")
        return NormalForm(poly_pow(base.num, node.exp, budget),
                          poly_pow(base.den, node.exp, budget),
                          base.domain_den)
    raise TypeError(f"未知节点类型：{type(node).__name__}")
