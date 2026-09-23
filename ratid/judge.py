"""恒等判断主流程。

结论只有三种：
  equivalent    —— 恒等：值相同 且 在有理数上的定义域相同；给出确定性证明依据
  not_equivalent—— 非等价：给出有理数反例并实际代入两个原始表达式复核
  unknown       —— 超出支持域 / 项数预算超限 / 反例搜索受限

关键原则：
  1. 只在随机点上相等绝不构成恒等证明；恒等靠“交叉相乘差为零多项式”的确定性判据。
  2. 约分结果相同不代表恒等：定义域由“原始除式”的有理根集合决定，必须相同。
  3. 全程 Fraction 精确计算，不使用符号代数库。
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from fractions import Fraction

from .errors import BudgetExceeded, Unsupported
from .normalize import (
    RationalForm,
    Step,
    domain_text,
    normalize_expression,
    poly_text,
    rational_text,
)
from .polynomial import eval_poly, multiply, rational_roots


class Verdict(str, Enum):
    EQUIVALENT = "equivalent"
    NOT_EQUIVALENT = "not_equivalent"
    UNKNOWN = "unknown"


@dataclass(frozen=True)
class Counterexample:
    x: Fraction
    kind: str                       # value_diff | domain_diff
    value_left: Fraction | None
    value_right: Fraction | None
    defined_left: bool
    defined_right: bool

    def describe(self) -> str:
        if self.kind == "domain_diff":
            only = "左" if self.defined_left and not self.defined_right else "右"
            return (
                f"定义域不同：x = {fmt_frac(self.x)} 处仅{only}侧有定义"
                f"（左有定义={self.defined_left}，右有定义={self.defined_right}）"
            )
        return (
            f"值不同：x = {fmt_frac(self.x)} 处 "
            f"左式 = {fmt_frac(self.value_left)}，"
            f"右式 = {fmt_frac(self.value_right)}"
        )


@dataclass(frozen=True)
class JudgeResult:
    left: str
    right: str
    verdict: Verdict
    reason: str
    steps: list[Step]
    excluded_left: dict[Fraction, int]
    excluded_right: dict[Fraction, int]
    counterexample: Counterexample | None
    checks: list[str]

    def is_equivalent(self) -> bool:
        return self.verdict is Verdict.EQUIVALENT


def fmt_frac(f: Fraction | None) -> str:
    if f is None:
        return "无定义"
    return str(f.numerator) if f.denominator == 1 else f"{f.numerator}/{f.denominator}"


def _excluded(roots: dict[Fraction, int]) -> str:
    if not roots:
        return "∅"
    return "{" + ", ".join(f"{fmt_frac(r)}(重数{m})" for r, m in sorted(roots.items())) + "}"


def _defined(rf: RationalForm, x: Fraction) -> bool:
    return eval_poly(rf.restrictions, x) != 0


def _eval_raw(rf: RationalForm, x: Fraction) -> Fraction:
    """在定义域内求值 P(x)/Q(x)；调用方需先确认 x 在定义域中。"""
    denom = eval_poly(rf.denominator, x)
    if denom == 0:
        raise ZeroDivisionError("约分后分母为零但原始除式未排除（不应发生）")
    return eval_poly(rf.numerator, x) / denom


def _verify_counterexample(
    x: Fraction, kind: str, left: RationalForm, right: RationalForm
) -> Counterexample:
    dl = _defined(left, x)
    dr = _defined(right, x)
    if kind == "domain_diff":
        if dl == dr:
            raise AssertionError("定义域反例复核失败")
        return Counterexample(x, kind, None, None, dl, dr)
    if not (dl and dr):
        raise AssertionError("值反例复核时发现点不在定义域中，改按定义域差异处理")
    vl = _eval_raw(left, x)
    vr = _eval_raw(right, x)
    if vl == vr:
        raise AssertionError("值反例复核失败：两式实际相等")
    return Counterexample(x, kind, vl, vr, dl, dr)


def _search_value_counterexample(
    delta_zero: bool, left: RationalForm, right: RationalForm, budget: int | None
) -> Counterexample | None:
    """非恒等有理函数一定存在有理反例：搜索小分母的有理数点并严格复核。"""
    if delta_zero:
        return None
    limit = 2 * (budget if budget is not None else 256)
    tried = 0
    for den in range(1, 65):
        numerators = [0] + [v for k in range(1, 65) for v in (k, -k)]
        for num in numerators:
            x = Fraction(num, den)
            if not (_defined(left, x) and _defined(right, x)):
                continue
            tried += 1
            if tried > limit:
                raise Unsupported("反例搜索触及上限，无法在有界范围内给出有理数反例")
            vl = _eval_raw(left, x)
            vr = _eval_raw(right, x)
            if vl != vr:
                return _verify_counterexample(x, "value_diff", left, right)
    return None


def judge(left_expr: str, right_expr: str, budget: int | None = 256) -> JudgeResult:
    """判断两个一元有理多项式表达式是否恒等。

    budget: 单次多项式乘法展开后的最大项数（默认 256）；超限返回 unknown。
    """
    steps: list[Step] = []
    try:
        left, left_steps = normalize_expression(left_expr, budget)
        right, right_steps = normalize_expression(right_expr, budget)
        steps.extend(left_steps)
        steps.extend(right_steps)
        checks: list[str] = []

        # 第一步：确定性的值判据——交叉相乘 P1*Q2 − P2*Q1 是否为零多项式。
        p1q2 = multiply(left.numerator, right.denominator, budget, "交叉相乘 P1*Q2")
        p2q1 = multiply(right.numerator, left.denominator, budget, "交叉相乘 P2*Q1")
        delta = p1q2 - p2q1
        delta_zero = delta.is_zero
        checks.append(
            f"交叉相乘判据：P1*Q2 − P2*Q1 = {poly_text(delta)}；"
            + ("为零多项式，故在两侧公共定义域上值恒等。" if delta_zero else
               "不是零多项式，故存在值不同的点。")
        )

        # 第二步：定义域判据——比较两侧原始除式的有理根集合（忽略重数）。
        roots_left = rational_roots(left.restrictions, budget)
        roots_right = rational_roots(right.restrictions, budget)
        set_l = set(roots_left)
        set_r = set(roots_right)
        checks.append(
            "定义域判据：左式排除的有理点 = "
            f"{_excluded(roots_left)}；右式 = {_excluded(roots_right)}；"
            + ("有理根集合相同，定义域相同。" if set_l == set_r else "有理根集合不同，定义域不同。")
        )
        checks.append(
            f"左式规范形：{rational_text(left)}；定义域 {domain_text(left.restrictions)}"
        )
        checks.append(
            f"右式规范形：{rational_text(right)}；定义域 {domain_text(right.restrictions)}"
        )

        if delta_zero and set_l == set_r:
            return JudgeResult(
                left_expr, right_expr, Verdict.EQUIVALENT,
                "交叉相乘差为零多项式（值恒等的充要条件），且两侧原始分母的"
                "有理零点集合完全相同（定义域相同）。证明为确定性代数判据，"
                "不依赖任何随机/定点代入。",
                steps, roots_left, roots_right, None, checks,
            )

        # 非等价：优先给值反例；值处处相等时给定义域反例。
        if not delta_zero:
            ce = _search_value_counterexample(False, left, right, budget)
            if ce is not None:
                reason = (
                    "交叉相乘差不是零多项式，因此两式不是同一有理函数；"
                    "已找到使两式都有定义但值不同的有理数点，并独立代入复核。"
                )
                return JudgeResult(
                    left_expr, right_expr, Verdict.NOT_EQUIVALENT, reason,
                    steps, roots_left, roots_right, ce, checks,
                )

        diff = sorted(set_l.symmetric_difference(set_r))
        if diff:
            x = diff[0]
            ce = _verify_counterexample(x, "domain_diff", left, right)
            reason = (
                "两式作为有理函数的约分形式相同，但语法上实际出现的分母"
                "零点集合不同：约分不能扩大真实定义域，故不恒等。"
            )
            return JudgeResult(
                left_expr, right_expr, Verdict.NOT_EQUIVALENT, reason,
                steps, roots_left, roots_right, ce, checks,
            )

        # 理论上不可达（delta 非零必有值反例），保守处理为未知。
        return JudgeResult(
            left_expr, right_expr, Verdict.UNKNOWN,
            "已确定非恒等，但在有界搜索内未能构造有理数反例，保守标记为未知。",
            steps, roots_left, roots_right, None, checks,
        )

    except BudgetExceeded as exc:
        return JudgeResult(
            left_expr, right_expr, Verdict.UNKNOWN,
            f"项数预算超限，停止展开并标记为未知（不猜测）：{exc}",
            steps, {}, {}, None, [],
        )
    except Unsupported as exc:
        return JudgeResult(
            left_expr, right_expr, Verdict.UNKNOWN,
            f"超出支持域，无法判断：{exc}",
            steps, {}, {}, None, [],
        )
