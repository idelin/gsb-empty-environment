"""把表达式 AST 精确规范化为有理形式，并记录每步变换。

对每个子表达式维护三元组 (P, Q, D)：
  - P/Q 是它作为有理函数的值（P、Q 为整系数多项式，Q 非零，已约去公因式）
  - D 是“原始除式的乘积”：
      * 不做任何约分，保留语法中实际出现的所有分母；
      * 适用域（在 Q 上）为 {x ∈ Q : D(x) ≠ 0}。
    这样分母定义域不同的表达式不会因约分结果相同而被误判。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import gcd

from .errors import BudgetExceeded, Unsupported
from .parser import Node
from .polynomial import (
    Poly,
    gcd_poly,
    multiply,
    power,
)

ZERO = Poly([0])
ONE = Poly([1])


@dataclass(frozen=True)
class RationalForm:
    numerator: Poly          # P：约简后的分子
    denominator: Poly        # Q：约简后的分母（非零）
    restrictions: Poly       # D：原始除式之积（未约分），定义域为 D != 0

    def is_zero_function(self) -> bool:
        return self.numerator.is_zero


@dataclass(frozen=True)
class Step:
    stage: str       # 原始片段 / 变换类型
    expr: str        # 子表达式
    detail: str      # 变换说明（含适用域）


def _lcm_int(values: list[int]) -> int:
    m = 1
    for v in values:
        m = m * v // gcd(m, v)
    return m


def _scale(poly: Poly, k_num: int, k_den: int) -> Poly:
    p = poly.mul_int(k_num) if k_num != 1 else poly
    if k_den != 1:
        for c in p.coeff:
            if c % k_den != 0:
                raise Unsupported("分数无法化为整系数")
        p = Poly([c // k_den for c in p.coeff])
    return p


def _reduce(p: Poly, q: Poly) -> tuple[Poly, Poly]:
    """约去分子分母的多项式公因式（不影响定义域跟踪，定义域另由 D 负责）。"""
    if q.is_zero:
        raise Unsupported("内部错误：分母为零多项式")
    if p.is_zero:
        return ZERO, ONE
    g = gcd_poly(p, q)
    if not g.is_const():
        qq, rq = _div_exact(q, g)
        pp, rp = _div_exact(p, g)
        if rq.is_zero and rp.is_zero:
            p, q = pp, qq
    # 保证分母首项系数为正
    if q.leading() < 0:
        p, q = -p, -q
    return p, q


def _div_exact(a: Poly, b: Poly) -> tuple[Poly, Poly]:
    """精确整除：商必为整系数；返回 (商, 余)。"""
    from .polynomial import divmod_poly

    q, r = divmod_poly(a, b)
    if not r.is_zero:
        return a, Poly.const(1)  # 非整除（理论上不会用于 GCD 结果）
    qc: list[int] = []
    for c in q.coeff:
        if isinstance(c, Fraction):
            if c.denominator != 1:
                return a, Poly.const(1)
            qc.append(int(c))
        else:
            qc.append(c)
    return Poly(qc), ZERO


def normalize(node: Node, budget: int | None, steps: list[Step]) -> RationalForm:
    """递归规范化。每一步都记录“做了什么变换”和它的适用域。"""
    kind = node.kind
    src = render_node(node)

    if kind == "num":
        f = node.value
        if f.denominator == 1:
            rf = RationalForm(Poly.const(f.numerator), ONE, ONE)
        else:
            # 有理常数直接作为 P/Q 进入有理形（分母为非零常数，处处有定义）
            rf = RationalForm(Poly.const(f.numerator), Poly.const(f.denominator), ONE)
        steps.append(Step("常量", src, "常量（含分数常数）本身处处有定义；适用域 = Q"))
        return rf

    if kind == "var":
        rf = RationalForm(Poly([0, 1]), ONE, ONE)
        steps.append(Step("变量", src, "变量 x 处处有定义；适用域 = Q"))
        return rf

    if kind == "neg":
        inner = normalize(node.left, budget, steps)
        rf = RationalForm(-inner.numerator, inner.denominator, inner.restrictions)
        steps.append(
            Step(
                "取负",
                src,
                "只改变分子符号，分母与定义域不变；"
                f"适用域 = {domain_text(inner.restrictions)}",
            )
        )
        return rf

    if kind in ("add", "sub", "mul", "div", "pow"):
        return normalize_compound(node, budget, steps, src)

    raise Unsupported(f"不支持的语法节点：{kind}")


def normalize_compound(
    node: Node, budget: int | None, steps: list[Step], src: str
) -> RationalForm:
    kind = node.kind

    if kind == "pow":
        n = int(node.value)
        base = normalize(node.left, budget, steps)
        if n >= 0:
            if n == 0:
                if base.numerator.is_zero:
                    raise Unsupported(f"遇到 0^0：{src}，该点无约定，超出支持域")
                rf = RationalForm(ONE, ONE, base.restrictions)
                detail = (
                    f"零次幂：{src} = 1（底数不是零函数）；"
                    f"适用域 = {domain_text(base.restrictions)}"
                )
            else:
                p = power(base.numerator, n, budget, "分子幂展开")
                q = power(base.denominator, n, budget, "分母幂展开")
                d = power(base.restrictions, n, budget, "原始除式幂展开")
                rf = RationalForm(p, q, d)
                detail = (
                    f"非负整数次幂 n={n}：分别对分子、分母取幂，"
                    f"原始除式随之取幂，不约去任何因子；适用域 = {domain_text(d)}"
                )
            steps.append(Step("整数幂", src, detail))
            return rf
        # 负整数次幂：等价于倒数后取 |n| 次幂
        if base.numerator.is_zero:
            raise Unsupported(f"零的负整数次幂：{src} 在任何点都无定义")
        npos = -n
        p = power(base.denominator, npos, budget, "倒数分子幂展开")
        q = power(base.numerator, npos, budget, "倒数字母幂展开")
        extra = power(base.numerator, npos, budget, "新增除式幂展开")
        d = multiply(base.restrictions, extra, budget, "定义域除式乘积")
        rf = RationalForm(p, q, d)
        steps.append(
            Step(
                "负整数幂",
                src,
                f"n={n}：先取倒数（新增除式为底数分子，不预约分），再取 {npos} 次幂；"
                f"适用域 = {domain_text(d)}",
            )
        )
        return rf

    left = normalize(node.left, budget, steps)
    right = normalize(node.right, budget, steps)

    if kind in ("add", "sub"):
        sign = 1 if kind == "add" else -1
        # P = P1*Q2 ± P2*Q1，Q = Q1*Q2；分母乘法全部展开
        p1q2 = multiply(left.numerator, right.denominator, budget, "通分乘法 P1*Q2")
        p2q1 = multiply(right.numerator, left.denominator, budget, "通分乘法 P2*Q1")
        p = p1q2 + p2q1.mul_int(sign) if sign == -1 else p1q2 + p2q1
        q = multiply(left.denominator, right.denominator, budget, "通分后的分母 Q1*Q2")
        p, q = _reduce(p, q)
        d = multiply(left.restrictions, right.restrictions, budget, "定义域除式乘积")
        op = "加法" if kind == "add" else "减法"
        steps.append(
            Step(
                op,
                src,
                f"通分：P = P1*Q2 {'+' if sign == 1 else '−'} P2*Q1，"
                f"Q = Q1*Q2（完整展开），再约去分子分母公因式；"
                f"注意：定义域不随约分扩大，仍为两侧原始除式非零；"
                f"适用域 = {domain_text(d)}",
            )
        )
        return RationalForm(p, q, d)

    if kind == "mul":
        p = multiply(left.numerator, right.numerator, budget, "分子乘法 P1*P2")
        q = multiply(left.denominator, right.denominator, budget, "分母乘法 Q1*Q2")
        p, q = _reduce(p, q)
        d = multiply(left.restrictions, right.restrictions, budget, "定义域除式乘积")
        steps.append(
            Step(
                "乘法",
                src,
                "P = P1*P2，Q = Q1*Q2（完整展开），再约去公因式；"
                "定义域取两侧原始除式非零之交；"
                f"适用域 = {domain_text(d)}",
            )
        )
        return RationalForm(p, q, d)

    # div：A/B。值为 P1*Q2 / (Q1*P2)；
    # 新增“原始除式” P2（除数自身的分子，不预约分），忠实记录语法分母。
    if right.numerator.is_zero:
        raise Unsupported(f"除以零：{src} 在任何点都无定义")
    p = multiply(left.numerator, right.denominator, budget, "除法分子 P1*Q2")
    q = multiply(left.denominator, right.numerator, budget, "除法分母 Q1*P2")
    p, q = _reduce(p, q)
    d1 = multiply(left.restrictions, right.restrictions, budget, "两侧旧除式乘积")
    d = multiply(d1, right.numerator, budget, "新增除数分子为原始除式")
    steps.append(
        Step(
            "除法",
            src,
            "值：P = P1*Q2，Q = Q1*P2（完整展开后约分）；"
            "但定义域必须把除数自身分子 P2 作为原始除式保留（不预约分），"
            "即要求 P2(x) ≠ 0，且两侧旧除式也非零；"
            f"适用域 = {domain_text(d)}",
        )
    )
    return RationalForm(p, q, d)


# ---------------------------------------------------------------------
# 展示辅助
# ---------------------------------------------------------------------


def domain_text(restrictions: Poly) -> str:
    if restrictions.is_const() and restrictions.coeff[0] != 0:
        return "Q（无限制）"
    return f"{{x ∈ Q : ({poly_text(restrictions)}) ≠ 0}}"


def poly_text(p: Poly) -> str:
    """把多项式转成可读字符串。"""
    if p.is_zero:
        return "0"
    parts: list[str] = []
    for exp in range(len(p.coeff) - 1, -1, -1):
        a = p.coeff[exp]
        if a == 0:
            continue
        sign = "+" if a > 0 else "−"
        av = abs(a)
        if exp == 0:
            term = str(av)
        elif exp == 1:
            term = "x" if av == 1 else f"{av}*x"
        else:
            term = f"x^{exp}" if av == 1 else f"{av}*x^{exp}"
        if not parts:
            parts.append(term if a > 0 else f"−{term}")
        else:
            parts.append(f" {sign} {term}")
    return "".join(parts)


def rational_text(rf: RationalForm) -> str:
    q = poly_text(rf.denominator)
    return f"({poly_text(rf.numerator)}) / ({q})"


def render_node(node: Node) -> str:
    """把 AST 还原成表达式字符串。"""
    k = node.kind
    if k == "num":
        return str(node.value)
    if k == "var":
        return "x"
    if k == "neg":
        return f"(-{render_node(node.left)})"
    if k == "pow":
        return f"({render_node(node.left)}^{node.value})"
    op = {"add": "+", "sub": "-", "mul": "*", "div": "/"}[k]
    return f"({render_node(node.left)} {op} {render_node(node.right)})"


def normalize_expression(text: str, budget: int | None = None) -> tuple[RationalForm, list[Step]]:
    from .parser import parse

    steps: list[Step] = []
    tree = parse(text)
    rf = normalize(tree, budget, steps)
    return rf, steps
