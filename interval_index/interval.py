"""整数区间的解析与关系判定。

区间语义（整数全集 Z）
----------------------
* 形如 ``[1,5)`` 的端点区间只支持整数端点，端点可为空：
  空端点表示该方向无界（开放端点），例如 ``[3,)``、``(,0]``、``(,)``。
* 端点标记：``[`` 表示含端点（闭），``]`` 表示含端点（闭）；
  ``(`` 与 ``)`` 表示不含端点（开）。
* 内部统一归一化为整数离散集合 ``{lo, lo+1, ..., hi}``：
  - 左端 ``[a`` => lo = a；``(a`` => lo = a + 1；左空端 => lo = -INF；
  - 右端 ``b]`` => hi = b；``b)`` => hi = b - 1；右空端 => hi = +INF。
* 点区间约定：``[x,x]`` 是合法的单点区间，只含整数 ``x``；
  ``[x,x)``、``(x,x]``、``(x,x)`` 归一化后为空区间。
* 不存在的区间（空集）记为 :attr:`Interval.empty`。
* 字段缺失或取值为空白记为“缺失”，用 :attr:`Interval.missing` 表示，
  它不是区间，不参与相交/包含/被包含匹配，只能被缺失查询选中。
"""

from __future__ import annotations

from dataclasses import dataclass

INF = 10**18
"""无界端的内部哨兵值；文档端点绝对值必须小于它。"""

INT_RE = __import__("re").compile(r"[+-]?\d+")


class IntervalError(ValueError):
    """区间字符串无法解析时抛出。"""


@dataclass(frozen=True, slots=True)
class Interval:
    """归一化后的整数区间 [lo, hi]（两端均闭，含端点）。

    lo == -INF 表示左无界；hi == INF 表示右无界；
    is_empty 为真表示空区间；is_missing 为真表示字段缺失（非区间）。
    """

    lo: int
    hi: int
    is_empty: bool = False
    is_missing: bool = False

    @classmethod
    def missing(cls) -> "Interval":
        return cls(lo=-INF, hi=INF, is_missing=True)

    @classmethod
    def empty(cls) -> "Interval":
        return cls(lo=INF, hi=-INF, is_empty=True)

    @classmethod
    def parse(cls, text: str) -> "Interval":
        """把 ``[1,5)`` 形式的字符串解析为 :class:`Interval`。"""
        if text is None:
            return cls.missing()
        s = text.strip()
        if not s:
            return cls.missing()
        if len(s) < 3 or s[0] not in "[](" or s[-1] not in "[])":
            raise IntervalError(f"区间括号不合法: {text!r}")
        inner = s[1:-1]
        parts = inner.split(",")
        if len(parts) != 2:
            raise IntervalError(f"区间必须恰好含一个逗号: {text!r}")
        left_raw, right_raw = parts[0].strip(), parts[1].strip()

        lo = _parse_endpoint(left_raw, is_left=True)
        if s[0] == "(" and left_raw != "":
            lo += 1
        hi = _parse_endpoint(right_raw, is_left=False)
        if s[-1] == ")" and right_raw != "":
            hi -= 1

        if lo > hi:
            return cls.empty()
        return cls(lo=lo, hi=hi)

    def covers(self, value: int) -> bool:
        """整数 value 是否属于该区间（空区间与缺失一律返回 False）。"""
        if self.is_empty or self.is_missing:
            return False
        return self.lo <= value <= self.hi


def _parse_endpoint(raw: str, *, is_left: bool) -> int:
    if raw == "":
        return -INF if is_left else INF
    m = INT_RE.fullmatch(raw)
    if m is None:
        raise IntervalError(f"端点必须是十进制整数或留空: {raw!r}")
    value = int(raw)
    if not (-INF < value < INF):
        raise IntervalError(f"端点超出允许范围 ±{INF}: {raw!r}")
    return value


def intersects(a: Interval, b: Interval) -> bool:
    """两个非空整数区间是否有公共整数点。

    离散整数上 [a.lo, a.hi] 与 [b.lo, b.hi] 相交当且仅当
    ``a.lo <= b.hi`` 且 ``b.lo <= a.hi``。例如 [0,5) 归一化为
    [0,4]，[5,8) 归一化为 [5,7]，二者不相交。
    """
    if a.is_missing or b.is_missing or a.is_empty or b.is_empty:
        return False
    return a.lo <= b.hi and b.lo <= a.hi


def contains(outer: Interval, inner: Interval) -> bool:
    """outer 是否包含 inner（inner 的每个整数点都在 outer 中）。

    空区间不包含任何区间；没有任何区间包含空区间以外的缺失值。
    """
    if outer.is_missing or inner.is_missing or outer.is_empty or inner.is_empty:
        return False
    return outer.lo <= inner.lo and inner.hi <= outer.hi


def contained_by(inner: Interval, outer: Interval) -> bool:
    """inner 是否被 outer 包含，:func:`contains` 的反向表述。"""
    return contains(outer, inner)
