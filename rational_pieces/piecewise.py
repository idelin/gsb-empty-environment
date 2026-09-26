"""分段有理函数：共同细分、来源映射、增量编辑、连续性判定。

设计要点：
- 组合（加/乘）时自动对交叠区间做共同细分，空洞（无定义区域）保持无定义；
- 每个细分段记录来源映射 sources（两侧片段 id）；
- 编辑片段后仅重算与其相关的细分段，其余段对象原样复用；
- 相邻同表达式段仅在“接点被覆盖且表达式在该点有定义”时合并，
  绝不跨越排除点合并；
- 连续性判定全部基于精确代数证据（精确极限、精确函数值、Sturm 根隔离）。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .interval import Interval
from .ratfunc import Limit, RationalFunction
from .roots import IsolationBudget, isolate_roots, rational_roots


# ---------------------------------------------------------------- 片段与分段

@dataclass
class Piece:
    pid: str
    interval: Interval
    func: RationalFunction


class Piecewise:
    """有序、互不重叠的分段有理函数（允许空洞，空洞即无定义）。"""

    def __init__(self, pieces):
        self.pieces = list(pieces)
        for i, p in enumerate(self.pieces):
            if not p.pid:
                p.pid = f"p{i}"
        self._validate()

    @classmethod
    def single(cls, interval: Interval, func: RationalFunction, pid: str = "p0") -> "Piecewise":
        return cls([Piece(pid, interval, func)])

    def _validate(self):
        for p in self.pieces:
            if p.interval.is_empty():
                raise ValueError(f"片段 {p.pid} 区间为空")
        for a, b in zip(self.pieces, self.pieces[1:]):
            if a.interval.hi is None or b.interval.lo is None:
                raise ValueError("区间未排序或存在重叠")
            if a.interval.hi > b.interval.lo:
                raise ValueError(f"片段 {a.pid} 与 {b.pid} 区间重叠")
            if (a.interval.hi == b.interval.lo
                    and a.interval.hi_closed and b.interval.lo_closed):
                raise ValueError(f"片段 {a.pid} 与 {b.pid} 在端点 {a.interval.hi} 处重叠")

    def get(self, pid: str) -> Piece:
        for p in self.pieces:
            if p.pid == pid:
                return p
        raise KeyError(pid)

    def replace(self, pid: str, interval: Interval, func: RationalFunction):
        """替换片段并保持有序不重叠；非法时抛出且不改状态。"""
        idx = next(i for i, p in enumerate(self.pieces) if p.pid == pid)
        old = self.pieces[idx]
        self.pieces[idx] = Piece(pid, interval, func)
        try:
            self._validate()
        except Exception:
            self.pieces[idx] = old
            raise

    def __iter__(self):
        return iter(self.pieces)

    def __len__(self):
        return len(self.pieces)

    def analyze(self, budget: IsolationBudget | None = None) -> "ContinuityReport":
        return analyze_continuity(self.pieces, budget)


# ---------------------------------------------------------------- 组合与增量维护

def _sort_key(iv: Interval):
    return (iv.lo is not None, iv.lo if iv.lo is not None else 0, not iv.lo_closed)


@dataclass
class Segment:
    """共同细分段：区间、组合表达式、来源映射。"""

    interval: Interval
    func: RationalFunction
    sources: tuple

    def __str__(self):
        return f"{self.interval} -> {self.func}  来源{list(self.sources)}"


class CombinedPiecewise:
    """两个 Piecewise 的组合（'add' 或 'mul'），带细分缓存与增量编辑。"""

    def __init__(self, left: Piecewise, right: Piecewise, op: str):
        if op not in ('add', 'mul'):
            raise ValueError("op 须为 'add' 或 'mul'")
        self.left, self.right, self.op = left, right, op
        pairs = [(lp, rp) for lp in left.pieces for rp in right.pieces]
        self._segments = self._merge(self._refine_pairs(pairs))

    def _combine(self, f, g):
        return f + g if self.op == 'add' else f * g

    def _refine_pairs(self, pairs):
        segs = []
        for lp, rp in pairs:
            iv = lp.interval.intersect(rp.interval)
            if iv is None:
                continue
            segs.append(Segment(iv, self._combine(lp.func, rp.func),
                                (f"L:{lp.pid}", f"R:{rp.pid}")))
        segs.sort(key=lambda s: _sort_key(s.interval))
        return segs

    @staticmethod
    def _merge(segs):
        """合并相邻同表达式段，但绝不跨越排除点（接点未被覆盖或表达式
        在接点无定义时不合并）。"""
        out = []
        for s in segs:
            if out:
                p = out[-1]
                j = p.interval.hi
                if (j is not None and j == s.interval.lo
                        and (p.interval.hi_closed or s.interval.lo_closed)
                        and p.func == s.func
                        and p.func.is_defined_at(j)):
                    iv = Interval(p.interval.lo, s.interval.hi,
                                  p.interval.lo_closed, s.interval.hi_closed)
                    out[-1] = Segment(iv, p.func, tuple(dict.fromkeys(p.sources + s.sources)))
                    continue
            out.append(s)
        return out

    @property
    def segments(self) -> list[Segment]:
        return list(self._segments)

    def edit(self, side: str, pid: str, *, interval: Interval = None,
             func: RationalFunction = None):
        """编辑一侧片段，增量维护共同细分与来源映射。

        仅重算与被编辑片段相关的细分段；其余段对象原样保留（可按 id 比对）。
        """
        if side not in ('L', 'R'):
            raise ValueError("side 须为 'L' 或 'R'")
        pw = self.left if side == 'L' else self.right
        piece = pw.get(pid)
        new_iv = interval if interval is not None else piece.interval
        new_func = func if func is not None else piece.func
        pw.replace(pid, new_iv, new_func)
        tag = f"{side}:{pid}"
        kept = [s for s in self._segments if tag not in s.sources]
        other = self.right if side == 'L' else self.left
        edited = pw.get(pid)
        pairs = [(edited, op) if side == 'L' else (op, edited) for op in other.pieces]
        new_segs = self._refine_pairs(pairs)
        merged = kept + new_segs
        merged.sort(key=lambda s: _sort_key(s.interval))
        self._segments = self._merge(merged)

    def analyze(self, budget: IsolationBudget | None = None) -> "ContinuityReport":
        pieces = [Piece(f"seg{i}<{','.join(s.sources)}>", s.interval, s.func)
                  for i, s in enumerate(self._segments)]
        return analyze_continuity(pieces, budget)


# ---------------------------------------------------------------- 连续性判定

@dataclass
class BoundaryReport:
    point: Fraction
    kind: str                     # continuous | jump | hole | infinite | one_sided | undecided
    left: Limit | None = None
    right: Limit | None = None
    value: Fraction | None = None
    defined: bool = False
    evidence: list = field(default_factory=list)

    def __str__(self):
        head = f"x = {self.point}: {self.kind}"
        if self.left is not None or self.right is not None:
            head += f"（左极限 {self.left}，右极限 {self.right}"
            head += f"，函数值 {self.value}）" if self.defined else "，该点无定义）"
        return head


@dataclass
class InteriorReport:
    pid: str
    kind: str                     # pole | removable | undecided
    location: str
    detail: str

    def __str__(self):
        return f"片段 {self.pid}: {self.kind} @ {self.location}（{self.detail}）"


_VERDICT_CN = {
    'continuous': '连续（所有内部边界左右极限与函数值精确相等，无内部奇异点）',
    'continuous_on_domain': '定义域内连续（存在仅单侧有定义的端点，不冒称连续）',
    'discontinuous': '不连续',
    'undecided': '未决（根隔离预算耗尽或信息不足，相关边界/内部点保留未决）',
}


@dataclass
class ContinuityReport:
    boundaries: list = field(default_factory=list)
    interiors: list = field(default_factory=list)
    verdict: str = 'continuous'

    @property
    def is_continuous_everywhere(self) -> bool:
        return self.verdict == 'continuous'

    def __str__(self):
        lines = [f"结论：{_VERDICT_CN[self.verdict]}"]
        for b in self.boundaries:
            lines.append(f"  边界 {b}")
            for e in b.evidence:
                lines.append(f"    - {e}")
        for r in self.interiors:
            lines.append(f"  内部 {r}")
        return "\n".join(lines)


def _endpoint_report(piece: Piece, c: Fraction, side: str, note: str) -> BoundaryReport:
    """定义域边缘端点：仅一侧有定义，不冒称连续；闭端点却无定义时报洞/无穷。"""
    lim = piece.func.limit_at(c, side)
    included = piece.interval.hi_closed if side == 'left' else piece.interval.lo_closed
    value = piece.func.value_at(c) if included else None
    defined = value is not None
    side_cn = '左' if side == 'left' else '右'
    rep = BoundaryReport(c, 'one_sided',
                         left=lim if side == 'left' else None,
                         right=lim if side == 'right' else None,
                         value=value, defined=defined)
    if not included:
        rep.evidence.append(f"{note}；端点开放，仅{side_cn}侧有定义，不冒称连续")
    elif defined:
        rep.evidence.append(f"{note}；仅{side_cn}侧有定义（{side_cn}侧极限 {lim}，"
                            f"函数值 {value}，精确求值），不冒称连续")
    else:
        rep.kind = 'hole' if lim.is_finite else 'infinite'
        rep.evidence.append(f"{note}；端点闭合但函数在该点无定义（定义域空洞），"
                            f"{side_cn}侧极限 {lim}")
    return rep


def _analyze_interior(piece: Piece, budget: IsolationBudget, interiors: list):
    iv = piece.interval
    checks = (
        ('pole', '极点（分母零点，极限无穷）', piece.func.simplified_den),
        ('removable', '可去奇点（极限存在但原式无定义）', piece.func.removable_factor()),
    )
    for kind, desc, fac in checks:
        if fac.degree < 1:
            continue
        iso = isolate_roots(fac, iv.lo, iv.hi, budget)
        # 端点处的根交由边界分析；此处只保留严格内部根
        if iv.hi is not None and fac.eval(iv.hi) == 0:
            iso.intervals = [(a, b) for (a, b) in iso.intervals if b != iv.hi]
        rr = rational_roots(fac) if iso.intervals else []
        for (a, b) in iso.intervals:
            exact = [r for r in rr if a < r <= b]
            if exact:
                loc = f"x = {exact[0]}（精确有理根）"
            else:
                loc = f"x ∈ ({a}, {b}]（Sturm 隔离区间，核验含唯一根）"
            interiors.append(InteriorReport(piece.pid, kind, loc, desc))
        for (a, b, k) in iso.undecided:
            interiors.append(InteriorReport(
                piece.pid, 'undecided', f"x ∈ ({a}, {b}]",
                f"根隔离预算耗尽，{k} 个{desc}未分离，保留未决"))


def _analyze_boundary(left: Piece, right: Piece, boundaries: list):
    a_hi, b_lo = left.interval.hi, right.interval.lo
    if a_hi < b_lo:
        boundaries.append(_endpoint_report(left, a_hi, 'left',
                                           f"间隙 ({a_hi}, {b_lo}) 无定义"))
        boundaries.append(_endpoint_report(right, b_lo, 'right',
                                           f"间隙 ({a_hi}, {b_lo}) 无定义"))
        return
    c = a_hi  # a_hi == b_lo，两片段在 c 相接
    L = left.func.limit_at(c, 'left')
    R = right.func.limit_at(c, 'right')
    value, defined = None, False
    if left.interval.hi_closed:
        v = left.func.value_at(c)
        if v is not None:
            value, defined = v, True
    if not defined and right.interval.lo_closed:
        v = right.func.value_at(c)
        if v is not None:
            value, defined = v, True
    rep = BoundaryReport(c, 'undecided', left=L, right=R, value=value, defined=defined)
    ev = rep.evidence
    ev.append(f"左极限 = {L}，右极限 = {R}（由约简式精确求值/极点符号判定）")
    if L.is_finite and R.is_finite:
        if L.value == R.value:
            if defined and value == L.value:
                rep.kind = 'continuous'
                ev.append(f"f({c}) = {value}（闭端点精确求值）；三者相等 ⟹ 连续")
            elif not defined:
                rep.kind = 'hole'
                ev.append(f"极限 {L.value} 存在，但 {c} 为排除点（两侧区间均不含或"
                          f"表达式在该点无定义）⟹ 可去间断，不把两侧合并")
            else:
                rep.kind = 'hole'
                ev.append(f"极限 {L.value} 存在，但函数值 {value} ≠ 极限 ⟹ 可去间断")
        else:
            rep.kind = 'jump'
            ev.append(f"左极限 {L.value} ≠ 右极限 {R.value} ⟹ 跳变，"
                      f"跳跃度 {R.value - L.value}")
    else:
        rep.kind = 'infinite'
        ev.append("至少一侧极限为无穷（极点）⟹ 无穷间断")
    boundaries.append(rep)


def analyze_continuity(pieces, budget: IsolationBudget | None = None) -> ContinuityReport:
    """对有序不重叠片段列做连续性判定，返回含精确代数证据的报告。"""
    budget = budget if budget is not None else IsolationBudget()
    pts = list(pieces)
    rep = ContinuityReport()
    for piece in pts:
        _analyze_interior(piece, budget, rep.interiors)
    for left, right in zip(pts, pts[1:]):
        _analyze_boundary(left, right, rep.boundaries)
    if pts:
        first, last = pts[0], pts[-1]
        if first.interval.lo is not None:
            rep.boundaries.append(_endpoint_report(first, first.interval.lo, 'right',
                                                   "定义域左端"))
        if last.interval.hi is not None:
            rep.boundaries.append(_endpoint_report(last, last.interval.hi, 'left',
                                                   "定义域右端"))
    rep.boundaries.sort(key=lambda b: b.point)
    kinds = {b.kind for b in rep.boundaries} | {r.kind for r in rep.interiors}
    if 'undecided' in kinds:
        rep.verdict = 'undecided'
    elif kinds & {'jump', 'hole', 'infinite', 'pole', 'removable'}:
        rep.verdict = 'discontinuous'
    elif 'one_sided' in kinds:
        rep.verdict = 'continuous_on_domain'
    else:
        rep.verdict = 'continuous'
    return rep
