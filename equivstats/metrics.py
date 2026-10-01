"""指标层：敏感值不同数、指定对数底的熵、误差区间阈值判定、失败类报告。

定义：
- 不同数（distinct count）：等价类内按 null 策略过滤后的敏感值类别数。
  同一值出现任意多次仍只计一种。
- 熵（entropy）：H = -sum_i p_i * log_b(p_i)，p_i = c_i / n，
  b 为配置的对数底（log_base），空类（过滤后无值）熵定义为 0。
- 误差区间：熵用 decimal 在两种精度下各算一次，差值加保守 slack 作为
  半宽，得到 [lo, hi]。精度可加倍提升（可加精度）。
- 阈值判定：lo >= 阈值 -> PASS；hi < 阈值 -> FAIL；否则 UNDECIDED（未决），
  提升精度后重判，达到精度上限仍未决则返回 UNDECIDED。
说明：误差区间是工程实用的数值界，不构成形式化证明。
"""

from __future__ import annotations

from collections import Counter
from dataclasses import dataclass, field
from decimal import Decimal, localcontext
from enum import Enum

from .core import Dataset, NullPolicy


class Verdict(Enum):
    PASS = "pass"
    FAIL = "fail"
    UNDECIDED = "undecided"


@dataclass(frozen=True)
class Interval:
    lo: Decimal
    hi: Decimal

    @property
    def width(self) -> Decimal:
        return self.hi - self.lo

    @property
    def midpoint(self) -> Decimal:
        return (self.lo + self.hi) / 2

    def decide(self, threshold: Decimal) -> Verdict:
        if self.lo >= threshold:
            return Verdict.PASS
        if self.hi < threshold:
            return Verdict.FAIL
        return Verdict.UNDECIDED


def filtered_items(counter: Counter, null_policy: NullPolicy) -> list[tuple]:
    return [
        (value, count)
        for value, count in counter.items()
        if value is not None or null_policy is NullPolicy.AS_CATEGORY
    ]


def distinct_count(counter: Counter, null_policy: NullPolicy) -> int:
    return len(filtered_items(counter, null_policy))


def class_total(counter: Counter, null_policy: NullPolicy) -> int:
    return sum(count for _, count in filtered_items(counter, null_policy))


def _entropy_decimal(counts: list[int], base: float, precision: int) -> Decimal:
    n = sum(counts)
    with localcontext() as ctx:
        ctx.prec = precision
        ln_base = Decimal(str(base)).ln()
        dn = Decimal(n)
        ln_n = dn.ln()
        total = Decimal(0)
        for count in counts:
            dc = Decimal(count)
            p = dc / dn
            total -= p * (dc.ln() - ln_n) / ln_base
        return +total


def _make_interval(center: Decimal, half_width: Decimal, precision: int) -> Interval:
    # 端点必须在足够精度的上下文中构造，否则微小半宽会被默认上下文舍入吞掉；
    # 熵非负，下界截断到 0。
    with localcontext() as ctx:
        ctx.prec = precision + 20
        lo = max(center - half_width, Decimal(0))
        hi = center + half_width
    return Interval(lo, hi)


def entropy_interval(counter: Counter, null_policy: NullPolicy, base: float,
                     precision: int = 28) -> Interval:
    counts = [count for _, count in filtered_items(counter, null_policy)]
    if len(counts) <= 1:
        # 空类或单一类别：熵恒为 0，返回精确零区间。
        return Interval(Decimal(0), Decimal(0))
    coarse = _entropy_decimal(counts, base, precision)
    fine = _entropy_decimal(counts, base, precision + 12)
    slack = Decimal(1).scaleb(-(precision - 6))
    half_width = abs(fine - coarse) + slack
    return _make_interval(fine, half_width, precision)


def decide_entropy(counter: Counter, null_policy: NullPolicy, base: float,
                   threshold, start_precision: int = 28,
                   max_precision: int = 120) -> tuple[Interval, Verdict]:
    """熵阈值判定：精度可加倍提升，仍无法区分则未决。"""
    threshold = Decimal(str(threshold))
    precision = start_precision
    while True:
        interval = entropy_interval(counter, null_policy, base, precision)
        verdict = interval.decide(threshold)
        if verdict is not Verdict.UNDECIDED or precision >= max_precision:
            return interval, verdict
        precision = min(precision * 2, max_precision)


@dataclass
class ValueContribution:
    value: object
    count: int
    proportion: Decimal
    entropy_contribution: Interval


@dataclass
class ClassReport:
    qid: tuple
    total: int
    distinct: int
    entropy: Interval
    distinct_ok: bool
    entropy_verdict: Verdict
    contributions: list = field(default_factory=list)

    @property
    def failing(self) -> bool:
        return not self.distinct_ok or self.entropy_verdict is not Verdict.PASS


def _contribution_point(count: int, n: int, base: float, precision: int) -> Decimal:
    with localcontext() as ctx:
        ctx.prec = precision
        p = Decimal(count) / Decimal(n)
        return +(-(p * (Decimal(count).ln() - Decimal(n).ln()) / Decimal(str(base)).ln()))


def _contribution_interval(count: int, n: int, base: float, precision: int) -> Interval:
    coarse = _contribution_point(count, n, base, precision)
    fine = _contribution_point(count, n, base, precision + 12)
    slack = Decimal(1).scaleb(-(precision - 6))
    half_width = abs(fine - coarse) + slack
    return _make_interval(fine, half_width, precision)


def class_report(qid: tuple, counter: Counter, null_policy: NullPolicy,
                 base: float, min_distinct: int, min_entropy,
                 precision: int = 28) -> ClassReport:
    items = filtered_items(counter, null_policy)
    n = sum(count for _, count in items)
    entropy, verdict = decide_entropy(counter, null_policy, base, min_entropy)
    contributions = []
    if n:
        with localcontext() as ctx:
            ctx.prec = precision
            for value, count in sorted(items, key=lambda item: (-item[1], str(item[0]))):
                proportion = Decimal(count) / Decimal(n)
                contributions.append(ValueContribution(
                    value=value,
                    count=count,
                    proportion=+proportion,
                    entropy_contribution=_contribution_interval(count, n, base, precision),
                ))
    return ClassReport(
        qid=qid,
        total=n,
        distinct=len(items),
        entropy=entropy,
        distinct_ok=len(items) >= min_distinct,
        entropy_verdict=verdict,
        contributions=contributions,
    )


def evaluate_dataset(dataset: Dataset, min_distinct: int, min_entropy,
                     precision: int = 28) -> list[ClassReport]:
    """输出每个等价类的报告；失败类及其各项贡献可直接过滤 failing。"""
    return [
        class_report(qid, counter, dataset.null_policy, dataset.log_base,
                     min_distinct, min_entropy, precision)
        for qid, counter in sorted(dataset.class_counts().items())
    ]
