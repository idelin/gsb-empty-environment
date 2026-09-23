"""多变量有理系数多项式的“同时代换”。

设计要点
--------
* 内部表示一：未展开的精确表达式 AST（Const / Var / Add / Mul / Pow），
  由智能构造子保持规范化（常数合并、幂合并、零吸收、变号折叠）。
* 内部表示二：展开的多项式 Poly = {单项式: Fraction}，
  单项式是排序后的 ((变量, 指数), ...)；系数一律用
  fractions.Fraction 精确表示（适用域：Q 上的交换多项式环）。
* 代换是“同时”的：一次性遍历 AST，只在叶子变量处做一次映射，
  映射右值整体植入、互不串扰。因此 x->y、y->x 是交换，不会全变成 x。
* 展开前先用纯结构性公式给出项数的保守上界（不构造任何单项式），
  上界超预算即停止，原样返回未展开的精确表达式；
  真正展开时再设硬上限双保险。预算中断永远不返回“截断的假等式”。
* 不依赖任何符号代数库，仅用 Python 3.11 标准库。
"""

from __future__ import annotations

import ast as _ast
import keyword
import math
from dataclasses import dataclass, field
from fractions import Fraction
from typing import Optional, Union

__all__ = [
    "Const", "Var", "Add", "Mul", "Pow",
    "const", "var", "add", "mul", "pow_", "neg", "sub",
    "parse", "replace_vars", "expand", "term_bound", "total_degree",
    "free_vars", "simultaneous_substitute", "substitute_string",
    "SubstResult", "Step", "BudgetExceeded", "Poly", "Expr",
    "format_expr", "format_poly", "eval_poly", "PolynomialError",
]


# ---------------------------------------------------------------------------
# 表达式 AST（未展开的精确形式）
# ---------------------------------------------------------------------------

class Expr:
    """所有表达式节点的基类（适用域：Q[x1,...,xn]，指数为非负整数）。"""


@dataclass(frozen=True)
class Const(Expr):
    value: Fraction


@dataclass(frozen=True)
class Var(Expr):
    name: str


@dataclass(frozen=True)
class Add(Expr):
    terms: tuple[Expr, ...]


@dataclass(frozen=True)
class Mul(Expr):
    factors: tuple[Expr, ...]


@dataclass(frozen=True)
class Pow(Expr):
    base: Expr
    exp: int


def const(value: Union[int, Fraction, str]) -> Const:
    if isinstance(value, Const):
        return value
    return Const(Fraction(value))


def var(name: str) -> Var:
    if not isinstance(name, str) or not name:
        raise ValueError("变量名必须是非空字符串")
    return Var(name)


def _key(e: Expr):
    """AST 的结构键：用于判重、合并、确定性排序。"""
    if isinstance(e, Const):
        return ("c", e.value.numerator, e.value.denominator)
    if isinstance(e, Var):
        return ("v", e.name)
    if isinstance(e, Add):
        return ("+", tuple(_key(t) for t in e.terms))
    if isinstance(e, Mul):
        return ("*", tuple(_key(f) for f in e.factors))
    if isinstance(e, Pow):
        return ("^", _key(e.base), e.exp)
    raise TypeError(f"未知节点类型: {type(e)!r}")


def _split_coef(e: Expr) -> tuple[Fraction, Expr]:
    """把 e 拆成 (常数系数 c, 去掉常数因子的主体)；Const(1) 表示纯常数。"""
    if isinstance(e, Const):
        return e.value, Const(Fraction(1))
    if isinstance(e, Mul):
        c = Fraction(1)
        rest: list[Expr] = []
        for f in e.factors:
            if isinstance(f, Const):
                c *= f.value
            else:
                rest.append(f)
        if not rest:
            body: Expr = Const(Fraction(1))
        elif len(rest) == 1:
            body = rest[0]
        else:
            body = Mul(tuple(rest))
        return c, body
    return Fraction(1), e


def add(*terms: Expr) -> Expr:
    """规范化加法：拍平、合并同类项（含 -e 与 +e 抵消）、丢零项。

    适用域：任意 Q[x1,...,xn] 中的元素；Q 特征为 0，故 c1+c2=0 可精确判定。
    """
    groups: dict = {}
    for t in terms:
        if isinstance(t, Add):  # 拍平嵌套加法
            sub_terms = t.terms
        else:
            sub_terms = (t,)
        for s in sub_terms:
            c, body = _split_coef(s)
            k = _key(body)
            if k in groups:
                groups[k] = (groups[k][0] + c, body)
            else:
                groups[k] = (c, body)
    kept = [(c, body) for c, body in groups.values() if c != 0]
    if not kept:
        return Const(Fraction(0))
    kept.sort(key=lambda cb: _key(cb[1]))
    out: list[Expr] = []
    for c, body in kept:
        if isinstance(body, Const):  # body 为 1：纯常数项
            out.append(Const(c))
        elif c == 1:
            out.append(body)
        else:
            out.append(Mul((Const(c), body)))
    if len(out) == 1:
        return out[0]
    return Add(tuple(out))


def mul(*factors: Expr) -> Expr:
    """规范化乘法：拍平、零吸收、常数合并、同底数幂合并。

    适用域：Q[x1,...,xn]；0*e=0（整环），交换律允许重排因子。
    """
    flat: list[Expr] = []

    def _flatten(e: Expr) -> None:
        if isinstance(e, Mul):
            for f in e.factors:
                _flatten(f)
        else:
            flat.append(e)

    for f in factors:
        _flatten(f)

    c = Fraction(1)
    bases: dict = {}
    for f in flat:
        if isinstance(f, Const):
            c *= f.value
            continue
        if isinstance(f, Pow):
            base, n = f.base, f.exp
        else:
            base, n = f, 1
        k = _key(base)
        if k in bases:
            bases[k] = (bases[k][0] + n, base)
        else:
            bases[k] = (n, base)

    if c == 0:
        return Const(Fraction(0))

    out: list[Expr] = []
    if c != 1:
        out.append(Const(c))
    for n, base in bases.values():
        if n == 0:
            continue
        out.append(base if n == 1 else Pow(base, n))
    if not out:
        return Const(Fraction(1))
    out.sort(key=_key)
    if len(out) == 1:
        return out[0]
    return Mul(tuple(out))


def pow_(base: Expr, n: int) -> Expr:
    """规范化幂。适用域：n 为非负整数；0**0 取空积约定 = 1。"""
    if isinstance(n, bool) or not isinstance(n, int) or n < 0:
        raise ValueError("多项式幂指数必须是非负整数")
    if isinstance(base, Const):
        return Const(base.value ** n)  # Fraction ** 非负整数仍是精确 Fraction
    if n == 0:
        return Const(Fraction(1))
    if n == 1:
        return base
    if isinstance(base, Pow):
        return Pow(base.base, base.exp * n)
    return Pow(base, n)


def neg(e: Expr) -> Expr:
    return mul(Const(Fraction(-1)), e)


def sub(a: Expr, b: Expr) -> Expr:
    return add(a, neg(b))


# ---------------------------------------------------------------------------
# 解析（白名单，只认精确有理运算；禁浮点、禁负指数）
# ---------------------------------------------------------------------------

class PolynomialError(ValueError):
    """输入不是 Q[x1,...,xn] 中的合法多项式。"""


def _const_int(node: _ast.AST) -> Optional[int]:
    if isinstance(node, _ast.UnaryOp) and isinstance(node.op, (_ast.UAdd, _ast.USub)):
        v = _const_int(node.operand)
        return None if v is None else (v if isinstance(node.op, _ast.UAdd) else -v)
    if isinstance(node, _ast.Constant) and isinstance(node.value, int) \
            and not isinstance(node.value, bool):
        return node.value
    return None


def parse(text: str) -> Expr:
    """解析多项式字符串，如 "(2/3)*x**2 + y - 1/2"。

    适用域：+ - * / ** 与括号、非负整数次幂；除法的分母必须是非零整数常数
    （如 x/2、2/3）；禁止浮点（会损失精确性）与负指数（越出多项式环）。
    """
    try:
        tree = _ast.parse(text, mode="eval")
    except SyntaxError as exc:
        raise PolynomialError(f"无法解析: {text!r}（{exc.msg}）") from None
    return _parse_node(tree.body)


def _parse_node(node: _ast.AST) -> Expr:
    if isinstance(node, _ast.BinOp):
        a = _parse_node(node.left)
        op = node.op
        if isinstance(op, _ast.Pow):
            if not (isinstance(node.right, _ast.Constant)
                    and isinstance(node.right.value, int)
                    and not isinstance(node.right.value, bool)
                    and node.right.value >= 0):
                raise PolynomialError("幂指数必须是非负整数常量")
            return pow_(a, node.right.value)
        b = _parse_node(node.right)
        if isinstance(op, _ast.Add):
            return add(a, b)
        if isinstance(op, _ast.Sub):
            return sub(a, b)
        if isinstance(op, _ast.Mult):
            return mul(a, b)
        if isinstance(op, _ast.Div):
            d = _const_int(node.right)
            if d is None or d == 0:
                raise PolynomialError("除法分母必须是非零整数常数（有理数域内精确计算）")
            return mul(a, Const(Fraction(1, d)))
        raise PolynomialError("不支持的二元运算")
    if isinstance(node, _ast.UnaryOp):
        if isinstance(node.op, _ast.UAdd):
            return _parse_node(node.operand)
        if isinstance(node.op, _ast.USub):
            return neg(_parse_node(node.operand))
        raise PolynomialError("不支持的一元运算")
    if isinstance(node, _ast.Name):
        if node.id.startswith("__") or keyword.iskeyword(node.id):
            raise PolynomialError(f"非法变量名: {node.id!r}")
        return Var(node.id)
    if isinstance(node, _ast.Constant):
        v = node.value
        if isinstance(v, bool):
            raise PolynomialError("布尔值不是合法的有理系数")
        if isinstance(v, int):
            return Const(Fraction(v))
        raise PolynomialError(f"只接受整数/有理常数，拒绝 {type(v).__name__}（浮点不精确）")
    raise PolynomialError(f"不支持的语法: {type(node).__name__}")


# ---------------------------------------------------------------------------
# AST 级变量替换（同时、一次性）
# ---------------------------------------------------------------------------

def replace_vars(e: Expr, mapping: dict[str, Expr]) -> Expr:
    """在叶子变量处同时替换。

    适用域：e ∈ Q[x1,...,xn]，映射值 ∈ Q[x1,...,xn]（允许含别的变量）。
    本函数是同态映射的直接实现：Var 叶子查一次表即停，绝不再对右值递归，
    因此多个映射互不污染（x->y 与 y->x 同时生效即为交换）。
    """
    if isinstance(e, Const):
        return e
    if isinstance(e, Var):
        got = mapping.get(e.name)
        return got if got is not None else e
    if isinstance(e, Add):
        return add(*(replace_vars(t, mapping) for t in e.terms))
    if isinstance(e, Mul):
        return mul(*(replace_vars(f, mapping) for f in e.factors))
    if isinstance(e, Pow):
        return pow_(replace_vars(e.base, mapping), e.exp)
    raise TypeError(type(e))


# ---------------------------------------------------------------------------
# 展开表示 Poly 与精确运算
# ---------------------------------------------------------------------------

# 单项式：排序后的 ((变量名, 指数), ...)；空元组是常数 1。
Monomial = tuple[tuple[str, int], ...]
Poly = dict  # dict[Monomial, Fraction]


class BudgetExceeded(Exception):
    """实际展开中项数硬超预算（保守上界漏判时的第二道防线）。"""


def _mon_mul(a: Monomial, b: Monomial) -> Monomial:
    merged: dict[str, int] = dict(a)
    for name, p in b:
        merged[name] = merged.get(name, 0) + p
    return tuple(sorted(merged.items()))


def _poly_add(a: Poly, b: Poly, budget: Optional[int]) -> Poly:
    out: Poly = dict(a)
    for m, c in b.items():
        out[m] = out.get(m, Fraction(0)) + c
        if out[m] == 0:
            del out[m]
    if budget is not None and len(out) > budget:
        raise BudgetExceeded(len(out))
    return out


def _poly_mul(a: Poly, b: Poly, budget: Optional[int]) -> Poly:
    if not a or not b:
        return {}
    out: Poly = {}
    for m1, c1 in a.items():
        for m2, c2 in b.items():
            m = _mon_mul(m1, m2)
            out[m] = out.get(m, Fraction(0)) + c1 * c2
            if out[m] == 0:
                del out[m]
    if budget is not None and len(out) > budget:
        raise BudgetExceeded(len(out))
    return out


def _poly_pow(p: Poly, n: int, budget: Optional[int]) -> Poly:
    """快速幂（平方次数 O(log n)）；适用域 n 为非负整数。"""
    result: Poly = {(): Fraction(1)}
    base = p
    while n:
        if n & 1:
            result = _poly_mul(result, base, budget)
        n >>= 1
        if n:
            base = _poly_mul(base, base, budget)
    return result


def expand(e: Expr, budget: Optional[int] = None) -> Poly:
    """完全展开为 {单项式: Fraction}。

    适用域：Q[x1,...,xn]。所有运算均为 Fraction 精确运算。
    若给出 budget，则中间任何一步项数超过它就抛 BudgetExceeded，
    不返回任何部分结果（调用方只能回退到未展开形式）。
    """
    if isinstance(e, Const):
        return {} if e.value == 0 else {(): e.value}
    if isinstance(e, Var):
        return {((e.name, 1),): Fraction(1)}
    if isinstance(e, Add):
        out: Poly = {}
        for t in e.terms:
            out = _poly_add(out, expand(t, budget), budget)
        return out
    if isinstance(e, Mul):
        out = {(): Fraction(1)}
        for f in e.factors:
            out = _poly_mul(out, expand(f, budget), budget)
            if not out:
                return {}
        return out
    if isinstance(e, Pow):
        if e.exp == 0:
            return {(): Fraction(1)}
        return _poly_pow(expand(e.base, budget), e.exp, budget)
    raise TypeError(type(e))


# ---------------------------------------------------------------------------
# 展开前的纯结构性项数上界（不生成单项式，几乎零成本）
# ---------------------------------------------------------------------------

def free_vars(e: Expr) -> set:
    if isinstance(e, Const):
        return set()
    if isinstance(e, Var):
        return {e.name}
    if isinstance(e, Add):
        out: set = set()
        for t in e.terms:
            out |= free_vars(t)
        return out
    if isinstance(e, Mul):
        out = set()
        for f in e.factors:
            out |= free_vars(f)
        return out
    if isinstance(e, Pow):
        return free_vars(e.base)
    raise TypeError(type(e))


def total_degree(e: Expr) -> int:
    """总次数上界。适用域：Q[x1,...,xn]；零多项式约定次数为 0。"""
    if isinstance(e, Const):
        return 0
    if isinstance(e, Var):
        return 1
    if isinstance(e, Add):
        return max((total_degree(t) for t in e.terms), default=0)
    if isinstance(e, Mul):
        return sum(total_degree(f) for f in e.factors)
    if isinstance(e, Pow):
        return total_degree(e.base) * e.exp
    raise TypeError(type(e))


def term_bound(e: Expr) -> int:
    """e 完全展开后不同单项式个数的保守上界（恒 >= 真实项数）。

    适用域：Q[x1,...,xn] 的任意表达式。规则（均为安全放大）：
      Const       -> 1（零多项式实际为 0，取 1 仍安全）
      Var         -> 1
      Add(t1..tk) -> sum B(ti)            （加法项数不超过各项之和）
      Mul(f1..fk) -> prod B(fi)           （乘法项数不超过笛卡尔积大小）
      Pow(b, n)   -> C(deg(b)*n + v, v)   （v 个变量、总次数 <= d 的单项式
                                            总数上界，d = deg(b)*n）
    只依赖结构，不构造单项式，因此估算本身不会膨胀。
    """
    if isinstance(e, (Const, Var)):
        return 1
    if isinstance(e, Add):
        return sum(term_bound(t) for t in e.terms)
    if isinstance(e, Mul):
        out = 1
        for f in e.factors:
            out *= term_bound(f)
        return out
    if isinstance(e, Pow):
        v = max(len(free_vars(e.base)), 1)
        d = total_degree(e.base) * e.exp
        return math.comb(d + v, v)
    raise TypeError(type(e))


# ---------------------------------------------------------------------------
# 主入口：同时代换 + 预算控制
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Step:
    """一步变换的记录：说明 + 适用域。"""
    note: str
    domain: str


@dataclass
class SubstResult:
    """代换结果。expanded=False 时 result 是未展开的精确 AST（绝非截断值）。"""
    original: Expr
    substituted: Expr
    mapping: dict
    expanded: bool
    result: Expr          # expanded 时也可由 poly 还原；未展开时即 substituted
    poly: Optional[Poly]  # 展开后的 {单项式: Fraction}；未展开为 None
    estimate: int
    budget: int
    stopped_by_budget: bool
    steps: list = field(default_factory=list)

    @property
    def terms(self) -> Optional[int]:
        return None if self.poly is None else len(self.poly)


def simultaneous_substitute(
    poly: Expr,
    mapping: dict,
    budget: int = 1000,
) -> SubstResult:
    """对多变量多项式做同时代换，按项数预算决定是否展开。

    步骤（每步附适用域）：
      1. 同时代换：叶子变量一次性映射，右值互不递归（Q[x1,...,xn] 同态）。
      2. 规范化：智能构造子即时化简；映射为 0 时零吸收律消去整项。
      3. 膨胀估计：term_bound 给出展开项数的保守上界（纯结构公式）。
      4. 预算裁决：上界 <= budget 才完全展开（Fraction 精确）；
         否则原样返回未展开 AST，绝不返回截断的假等式。
    """
    if budget < 0:
        raise ValueError("预算必须是非负整数")
    steps: list[Step] = []

    steps.append(Step(
        note="同时代换：一次性遍历，叶子变量各查一次映射表，右值整体植入、互不递归",
        domain="Q[x1,...,xn] 上的环同态；映射值可为任意多项式（含变量）",
    ))
    mapping = dict(mapping)
    substituted = replace_vars(poly, mapping)

    steps.append(Step(
        note="规范化：常数合并、同类项合并、幂合并；映射为 0 的因子按零吸收律消去整项",
        domain="Q[x1,...,xn]；Q 特征为 0，系数相等/相消均可精确判定",
    ))

    estimate = term_bound(substituted)
    steps.append(Step(
        note=f"膨胀估计：展开后项数 <= {estimate}（保守上界，纯结构公式，未生成单项式）",
        domain="组合上界：加法取和、乘法取积、幂取 C(总次数+变量数, 变量数)",
    ))

    if estimate <= budget:
        try:
            poly_dict = expand(substituted, budget=budget)
        except BudgetExceeded:
            # 双保险：理论上保守上界不超预算就不会走到这里
            steps.append(Step(
                note="展开中途项数硬超预算，放弃展开，回退到未展开形式",
                domain="任何预算策略；结果仍是精确等式，只是保持未展开形态",
            ))
            return SubstResult(poly, substituted, mapping, False, substituted,
                               None, estimate, budget, True, steps)
        steps.append(Step(
            note=f"完全展开：{len(poly_dict)} 项，系数全部为精确有理数",
            domain="Q[x1,...,xn]；单项式按变量名排序，系数为 fractions.Fraction",
        ))
        return SubstResult(poly, substituted, mapping, True, substituted,
                           poly_dict, estimate, budget, False, steps)

    steps.append(Step(
        note=f"上界 {estimate} 超过预算 {budget}，停止展开，返回未展开的精确形式",
        domain="预算策略；未展开 AST 与原多项式严格相等，不是截断近似",
    ))
    return SubstResult(poly, substituted, mapping, False, substituted,
                       None, estimate, budget, True, steps)


def substitute_string(text: str, mapping: dict, budget: int = 1000) -> SubstResult:
    """字符串便捷入口：mapping 的值可以是 Expr 或多项式字符串。"""
    expr = parse(text)
    parsed = {name: (val if isinstance(val, Expr) else parse(str(val)))
              for name, val in mapping.items()}
    return simultaneous_substitute(expr, parsed, budget=budget)


# ---------------------------------------------------------------------------
# 打印与求值
# ---------------------------------------------------------------------------

def _fmt_coef(c: Fraction) -> str:
    return str(c.numerator) if c.denominator == 1 else f"{c.numerator}/{c.denominator}"


def _fmt_monomial(m: Monomial) -> str:
    return "*".join(f"{name}**{p}" if p != 1 else name for name, p in m)


def format_poly(p: Poly) -> str:
    """把展开的多项式打印为确定顺序的字符串（单项式按字典序）。"""
    if not p:
        return "0"
    parts = []
    for m in sorted(p):
        c = p[m]
        if not m:
            parts.append(_fmt_coef(c))
        elif c == 1:
            parts.append(_fmt_monomial(m))
        elif c == -1:
            parts.append("-" + _fmt_monomial(m))
        else:
            parts.append(f"{_fmt_coef(c)}*{_fmt_monomial(m)}")
    out = parts[0]
    for part in parts[1:]:
        if part.startswith("-"):
            out += " - " + part[1:]
        else:
            out += " + " + part
    return out


def _paren(s: str, need: bool) -> str:
    return f"({s})" if need else s


def format_expr(e: Expr) -> str:
    """打印（可能未展开的）表达式 AST。"""
    if isinstance(e, Const):
        return _fmt_coef(e.value)
    if isinstance(e, Var):
        return e.name
    if isinstance(e, Add):
        pieces = []
        for t in e.terms:
            c, body = _split_coef(t)
            if isinstance(body, Const):  # 纯常数项
                pieces.append((_fmt_coef(c), c < 0))
            else:
                body_s = format_expr(body)
                if c == 1:
                    pieces.append((body_s, False))
                elif c == -1:
                    pieces.append((body_s, True))
                else:
                    pieces.append((f"{_fmt_coef(c)}*{body_s}", c < 0))
        first_s, first_neg = pieces[0]
        out = ("-" if first_neg else "") + first_s
        for s, is_neg in pieces[1:]:
            out += (" - " if is_neg else " + ") + s
        return out
    if isinstance(e, Mul):
        parts = []
        for f in e.factors:
            s = format_expr(f)
            need = isinstance(f, Add)
            parts.append(_paren(s, need))
        return "*".join(parts)
    if isinstance(e, Pow):
        s = format_expr(e.base)
        need = isinstance(e.base, (Add, Mul)) or (
            isinstance(e.base, Const) and e.base.value < 0)
        return f"{_paren(s, need)}**{e.exp}"
    raise TypeError(type(e))


def eval_poly(p: Poly, point: dict) -> Fraction:
    """在有理点处求值（适用域：point 覆盖 p 的全部变量，值为有理数）。"""
    total = Fraction(0)
    for m, c in p.items():
        term = c
        for name, power in m:
            term *= Fraction(point[name]) ** power
        total += term
    return total
