"""独立检验器：用有理点枚举验证复合关系 h == g ∘ f。

采样点只取自断点（段端点与内部均分点），不逐 tick 展开，
因此对 [0, 10**18) 这样的巨大区间同样瞬时完成。
所有比较都是 Fraction 精确相等。
"""
from __future__ import annotations

from fractions import Fraction


def sample_points(mapping):
    """按断点枚举有理检验点：每段的闭端点加内部 1/4、2/4、3/4 均分点。"""
    pts = []
    for seg in mapping.segments:
        d = seg.domain
        if d.lo == d.hi:
            pts.append(d.lo)
            continue
        span = d.hi - d.lo
        for k in (1, 2, 3):
            pts.append(d.lo + span * Fraction(k, 4))
        if d.lo_closed:
            pts.append(d.lo)
        if d.hi_closed:
            pts.append(d.hi)
    return pts


def verify_composition(f, g, h):
    """检验 h == g ∘ f：对 h 的断点采样 x，精确比较 h(x) 与 g(f(x))。

    返回失配描述列表；空列表表示检验通过。
    """
    errors = []
    for x in sample_points(h):
        hv = h.value_at(x)
        fv = f.value_at(x)
        gv = g.value_at(fv) if fv is not None else None
        if hv is None or fv is None or gv is None or hv != gv:
            errors.append(f"x={x}: h(x)={hv}, g(f(x))={gv}")
    return errors
