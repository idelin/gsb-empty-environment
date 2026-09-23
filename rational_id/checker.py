"""有理多项式表达式恒等判定主流程。

方法要点（对应题面要求）：
- 随机点相等不等于恒等：恒等由规范化后的精确多项式比较（交叉相乘）
  得出，从不通过随机/网格点取值证明；点求值只用于构造并验证反例。
- 分母定义域不同不能仅看约分结果：除比较有理函数是否相同外，还比较
  两边未约分“定义域分母”的零点集（无平方部分）。
- 有理数精确计算：全程 fractions.Fraction，不调用符号代数库。
- 超出支持域或项数预算时返回“未知”，不猜测。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from itertools import count, product

from .normalize import normalize
from .parser import ParseError, UnsupportedError, parse
from .poly import (BudgetExceeded, Poly, TermBudget, add, mul,
                   poly_to_str, same_up_to_scalar, squarefree_part, sub)

IDENTICAL = "identical"
NOT_IDENTICAL = "not_identical"
UNKNOWN = "unknown"

VERDICT_ZH = {IDENTICAL: "恒等", NOT_IDENTICAL: "不恒等", UNKNOWN: "未知"}


@dataclass
class CheckResult:
    verdict: str
    reason: str
    log: list = field(default_factory=list)
    forms: dict | None = None
    counterexample: dict | None = None


def _rational_axis():
    """按高度递增枚举有理数：0, -1, 1, -2, 2, -1/2, 1/2, -3, 3, ..."""
    zero = Fraction(0)
    yield zero
    seen = {zero}
    for height in count(1):
        for q in range(1, height + 1):
            for p in range(-height, height + 1):
                value = Fraction(p, q)
                if value not in seen:
                    seen.add(value)
                    yield value


def _points(nvars):
    """按对角顺序枚举 Q^n，任一有理点最终都会被枚举到。"""
    if nvars == 0:
        yield ()
        return
    axis = []
    gen = _rational_axis()
    for m in count(0):
        while len(axis) <= m:
            axis.append(next(gen))
        for idx_tuple in product(range(m + 1), repeat=nvars):
            if max(idx_tuple) == m:
                yield tuple(axis[i] for i in idx_tuple)


def _find_point(nvars, predicate, max_evals):
    for i, point in enumerate(_points(nvars)):
        if i >= max_evals:
            return None
        if predicate(point):
            return point
    return None


def _verify_point(ast_left, ast_right, names, point):
    """在见证点上对两个原始表达式精确求值并验证它们确实可区分。"""
    env = dict(zip(names, point))

    def try_eval(node):
        try:
            return node.eval(env)
        except ZeroDivisionError:
            return None

    left_val = try_eval(ast_left)
    right_val = try_eval(ast_right)
    if left_val is None and right_val is None:
        raise AssertionError("见证点验证失败：两边均无定义")
    if left_val is not None and right_val is not None and left_val == right_val:
        raise AssertionError("见证点验证失败：两边取值相同")
    return {
        "point": {name: str(value) for name, value in env.items()},
        "left": "无定义（除数为零）" if left_val is None else str(left_val),
        "right": "无定义（除数为零）" if right_val is None else str(right_val),
        "verified": True,
    }


def _forms_dict(names, form_left, form_right):
    def one(form):
        return {
            "num": poly_to_str(form.num, names),
            "den": poly_to_str(form.den, names),
            "domain_den": poly_to_str(form.domain_den, names),
        }

    return {"variables": names, "left": one(form_left), "right": one(form_right)}


def check(source_left, source_right, term_budget=2000, eval_budget=20000):
    log = [
        "支持域：仅含 + - * / ^（非负整数次幂）、有理数常量、变量、括号；"
        "超出支持域即返回未知",
    ]

    try:
        ast_left = parse(source_left)
        ast_right = parse(source_right)
    except (ParseError, UnsupportedError) as exc:
        log.append(f"解析失败：{exc}；超出支持域，返回未知")
        return CheckResult(UNKNOWN, f"超出支持域或语法错误：{exc}", log)

    names = sorted(ast_left.variables() | ast_right.variables())
    var_index = {name: i for i, name in enumerate(names)}
    nvars = len(names)
    budget = TermBudget(term_budget)

    try:
        form_left = normalize(ast_left, var_index, nvars, budget, log)
        form_right = normalize(ast_right, var_index, nvars, budget, log)
    except BudgetExceeded as exc:
        log.append(f"展开中止：{exc}；按规则返回未知")
        return CheckResult(UNKNOWN, f"项数预算超限：{exc}", log)

    forms = _forms_dict(names, form_left, form_right)
    dom_l = form_left.domain_den
    dom_r = form_right.domain_den

    # 退化情形：某表达式的定义域分母是零多项式（处处无定义）
    if dom_l.is_zero() or dom_r.is_zero():
        if dom_l.is_zero() and dom_r.is_zero():
            log.append("两边定义域分母均为零多项式：两边处处无定义")
            return CheckResult(
                IDENTICAL, "两式均处处无定义（定义域均为空集），平凡恒等", log, forms)
        if dom_l.is_zero():
            zero_side, other_dom, other_n = "左式", dom_r, "右式"
        else:
            zero_side, other_dom, other_n = "右式", dom_l, "左式"
        point = _find_point(nvars, lambda p: other_dom.evaluate(p) != 0, eval_budget)
        if point is None:
            return CheckResult(
                UNKNOWN,
                f"{zero_side}处处无定义，但未在求值预算内找到{other_n}有定义的点",
                log, forms)
        cex = _verify_point(ast_left, ast_right, names, point)
        return CheckResult(
            NOT_IDENTICAL,
            f"{zero_side}处处无定义，而{other_n}在 {cex['point']} 有定义",
            log, forms, cex)

    # 第一步：作为有理函数是否相同（交叉相乘，精确多项式恒等）
    log.append(
        "比较有理函数：交叉相乘 n1·d2 − n2·d1，判断是否为零多项式；"
        f"适用域：有理函数域 Q({','.join(names) if names else ''}) 中的恒等，"
        "等价于在两式共同定义域上处处相等；不依赖随机点取值")
    try:
        diff = sub(mul(form_left.num, form_right.den, budget),
                   mul(form_right.num, form_left.den, budget), budget)
    except BudgetExceeded as exc:
        log.append(f"交叉相乘展开中止：{exc}；按规则返回未知")
        return CheckResult(UNKNOWN, f"项数预算超限：{exc}", log, forms)

    if not diff.is_zero():
        log.append(
            f"交叉相乘结果非零（{poly_to_str(diff, names)}），两式不是同一有理函数")
        point = _find_point(
            nvars,
            lambda p: (dom_l.evaluate(p) != 0
                       and dom_r.evaluate(p) != 0
                       and diff.evaluate(p) != 0),
            eval_budget)
        if point is None:
            return CheckResult(
                UNKNOWN,
                "两式作为有理函数不同，但未在求值预算内找到有理反例点",
                log, forms)
        cex = _verify_point(ast_left, ast_right, names, point)
        return CheckResult(
            NOT_IDENTICAL, "两式在共同定义域内某有理点取值不同",
            log, forms, cex)

    log.append("交叉相乘结果为零多项式：两式是同一有理函数（共同定义域上处处相等）")

    # 第二步：比较定义域（未约分的定义域分母之零点集）
    log.append(
        "比较定义域：取两边定义域分母（未约分）的无平方部分；"
        "适用域：Q 上零点集相同 ⟺ 无平方部分相差非零常数倍；"
        "不能只看约分后的最简分母")
    try:
        sq_l = squarefree_part(dom_l, budget)
        sq_r = squarefree_part(dom_r, budget)
    except BudgetExceeded as exc:
        log.append(f"无平方部分计算中止：{exc}；按规则返回未知")
        return CheckResult(UNKNOWN, f"项数预算超限：{exc}", log, forms)

    if same_up_to_scalar(sq_l, sq_r):
        log.append("两边定义域分母的无平方部分相同：定义域一致")
        return CheckResult(
            IDENTICAL, "有理函数相同，且定义域相同（定义域分母的无平方部分一致）",
            log, forms)

    log.append(
        "定义域分母无平方部分不同："
        f"左 {poly_to_str(sq_l, names)}，右 {poly_to_str(sq_r, names)}")
    point = _find_point(
        nvars,
        lambda p: (dom_l.evaluate(p) == 0) != (dom_r.evaluate(p) == 0),
        eval_budget)
    if point is None:
        return CheckResult(
            UNKNOWN,
            "定义域在代数上不同，但未在求值预算内找到有理见证点"
            "（可能存在但超出预算，或在有理点上两定义域相同）",
            log, forms)
    cex = _verify_point(ast_left, ast_right, names, point)
    return CheckResult(
        NOT_IDENTICAL,
        "两式定义域不同：存在有理点使恰好一边无定义（仅有理函数相同是不够的）",
        log, forms, cex)
