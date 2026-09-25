"""跨边界直方图操作：并集（union）与同样本调和（reconcile）。

两种操作共享同一套"非负整数计数约束"机制：

- union(h1, h2, ...)：合并**不同样本**（如不同分片）用不同固定边界
  统计的直方图。每个源桶落入细化原子的拆分无法精确得知，
  用非负整数变量表达未知分布（不假定桶内均匀，不做按比例重分桶）。
  分位数返回所有相容拆分共同覆盖的可靠界限，并给出达到端点的见证。

- reconcile(h1, h2, ...)：调和**同一样本**被不同边界统计的多个直方图。
  若约束系统不存在非负整数解（统计不相容），构造时即拒绝。
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Sequence, Tuple, Union

from histogram import (NEG_INF, POS_INF, FixedHistogram, HistogramError,
                       IncompatibleHistograms, Number, _fmt, parse_q, rank_of)


@dataclass(frozen=True)
class Atom:
    """原子区间 [lo, hi)，由所有源边界的并集细分得到。"""

    lo: Number
    hi: Number

    def left_rep(self) -> Number:
        """落在本原子内、尽量靠左的代表值。"""
        if self.lo != NEG_INF:
            return self.lo
        if self.hi != POS_INF:
            return self.hi - 1
        return 0

    def right_rep(self) -> Number:
        """落在本原子内、尽量靠右的代表值（小于右端点的最大值）。"""
        if self.hi == POS_INF:
            return self.left_rep()
        if isinstance(self.lo, int) and isinstance(self.hi, int):
            return self.hi - 1
        rep = math.nextafter(float(self.hi), float("-inf"))
        if self.lo != NEG_INF and rep < self.lo:
            rep = self.lo
        return rep

    def __str__(self) -> str:
        return f"[{_fmt(self.lo)}, {_fmt(self.hi)})"


class _Lin:
    """线性约束: lo <= sum(values[atoms]) <= hi。"""

    __slots__ = ("atoms", "lo", "hi", "pos", "suffix")

    def __init__(self, atoms, lo, hi):
        self.atoms = tuple(sorted(atoms))
        self.lo = lo
        self.hi = hi


def _solve(num_vars: int, constraints: List[_Lin]) -> Optional[Tuple[int, ...]]:
    """精确的非负整数可行性求解（DFS + 区间约束传播）。

    等式约束占主导时首试上界即贪心解，通常无需回溯。
    返回一个解（按变量下标排列的计数元组）或 None。
    """
    static_ub: List[Optional[int]] = [None] * num_vars
    for c in constraints:
        for a in c.atoms:
            u = static_ub[a]
            static_ub[a] = c.hi if u is None else min(u, c.hi)
    cap = sum(c.hi for c in constraints)
    static_ub = [cap if u is None else u for u in static_ub]

    for c in constraints:
        c.pos = {a: i for i, a in enumerate(c.atoms)}
        suf = [0] * (len(c.atoms) + 1)
        for i in range(len(c.atoms) - 1, -1, -1):
            suf[i] = suf[i + 1] + static_ub[c.atoms[i]]
        c.suffix = suf

    var_cons: List[List[int]] = [[] for _ in range(num_vars)]
    for ci, c in enumerate(constraints):
        for a in c.atoms:
            var_cons[a].append(ci)

    assigned = [0] * len(constraints)
    values = [0] * num_vars

    def dfs(k: int) -> bool:
        if k == num_vars:
            return all(c.lo <= assigned[ci] <= c.hi
                       for ci, c in enumerate(constraints))
        lb = 0
        ub = static_ub[k]
        for ci in var_cons[k]:
            c = constraints[ci]
            rest = c.suffix[c.pos[k] + 1]  # 该约束后续变量最多还能贡献的量
            ub = min(ub, c.hi - assigned[ci])
            lb = max(lb, c.lo - assigned[ci] - rest)
        lb = max(lb, 0)
        if lb > ub or ub < 0:
            return False
        v = ub
        while v >= lb:
            values[k] = v
            for ci in var_cons[k]:
                assigned[ci] += v
            if dfs(k + 1):
                return True
            for ci in var_cons[k]:
                assigned[ci] -= v
            v -= 1
        return False

    return tuple(values) if dfs(0) else None


@dataclass(frozen=True)
class QuantileBound:
    """分位数可靠界限。

    lo/hi：所有相容分布的 rank 值共同覆盖的区间 [lo, hi)。
    lo_atom/hi_atom：端点原子下标；low_witness/high_witness：
    达到两个端点原子的具体相容分布（非负整数计数向量）。
    """

    q: Fraction
    rank: int
    n: int
    lo: Number
    hi: Number
    lo_atom: int
    hi_atom: int
    low_witness: Tuple[int, ...]
    high_witness: Tuple[int, ...]
    explanation: str

    def __str__(self) -> str:
        return (f"QuantileBound(q={self.q}, rank={self.rank}/{self.n}, "
                f"[{_fmt(self.lo)}, {_fmt(self.hi)}), "
                f"atoms={self.lo_atom}..{self.hi_atom})")


class _ConstraintView:
    """约束视图基类：原子、变量与等式约束，提供分位数界限与见证展开。"""

    def _init(self, atoms, var_atom, equalities, nulls, nans, total, sources):
        self.atoms: List[Atom] = list(atoms)
        # 变量 v 属于原子 var_atom[v]；等式约束公开，便于独立枚举验证
        self.var_atom: Tuple[int, ...] = tuple(var_atom)
        self.equalities: List[Tuple[Tuple[int, ...], int]] = [
            (tuple(idx), t) for idx, t in equalities]
        self.nulls = nulls
        self.nans = nans
        self.total = total
        self.sources: Tuple[FixedHistogram, ...] = tuple(sources)
        self._constraints = [_Lin(idx, t, t) for idx, t in self.equalities]
        atom_vars: List[List[int]] = [[] for _ in self.atoms]
        for v, a in enumerate(self.var_atom):
            atom_vars[a].append(v)
        self._prefix_vars: List[Tuple[int, ...]] = []
        acc: List[int] = []
        for vs in atom_vars:
            acc = acc + vs
            self._prefix_vars.append(tuple(acc))

    # ------------------------------------------------------------------
    def quantile(self, q: Union[int, float, str, Fraction]) -> QuantileBound:
        """所有相容分布共同覆盖的 q 分位数可靠界限（含端点见证）。"""
        qf = parse_q(q)
        n = self.total
        r = rank_of(qf, n)
        m = len(self.atoms)
        reachable: List[int] = []
        witnesses = {}
        for k in range(m):
            through = self._prefix_vars[k]
            before = self._prefix_vars[k - 1] if k else ()
            extra = [_Lin(before, 0, r - 1),   # 原子 k 之前至多 r-1 个
                     _Lin(through, r, n)]      # 到原子 k 为止至少 r 个
            sol = _solve(len(self.var_atom), self._constraints + extra)
            if sol is not None:
                reachable.append(k)
                witnesses[k] = sol
        if not reachable:  # 构造时已验证可行，理论上不会到这里
            raise HistogramError("内部错误：无可达原子")
        lo_atom, hi_atom = reachable[0], reachable[-1]
        lo, hi = self.atoms[lo_atom].lo, self.atoms[hi_atom].hi
        expl = (
            f"秩 r=max(1,ceil(q*n))={r}，总体 n={n}（已排除 null={self.nulls}、"
            f"NaN={self.nans}）。在全部相容的非负整数分布上，第 {r} 个有序值"
            f"可能落在原子 {lo_atom}..{hi_atom}，故可靠界限为 "
            f"[{_fmt(lo)}, {_fmt(hi)})；该界限覆盖所有相容分布，"
            f"两个端点原子分别由 low_witness/high_witness 中的具体分布实现。"
            f"未做桶内均匀假定，也未使用按比例重分桶估计，故不提供点估计；"
            f"区间宽度即误差上界。")
        return QuantileBound(qf, r, n, lo, hi, lo_atom, hi_atom,
                             witnesses[lo_atom], witnesses[hi_atom], expl)

    # ------------------------------------------------------------------
    def atom_totals(self, assignment: Sequence[int]) -> List[int]:
        """把变量层面的见证归约为各原子的总计数。"""
        if len(assignment) != len(self.var_atom):
            raise ValueError("见证长度必须等于变量数")
        totals = [0] * len(self.atoms)
        for v, cnt in enumerate(assignment):
            totals[self.var_atom[v]] += cnt
        return totals

    def expand_witness(self, assignment: Sequence[int],
                       strategy: str = "left") -> List[Number]:
        """把见证分布展开为代表值样本（仅适合小域演示/验证）。

        strategy="left"：每个原子取左代表值，样本的 rank 值达到界限左端点；
        strategy="right"：每个原子取右代表值，样本的 rank 值逼近界限右端点
        （右端为开区间，只能逼近不能达到）。展开样本用任一源边界重新统计，
        都会精确还原该源直方图。
        """
        totals = self.atom_totals(assignment)
        rep = Atom.left_rep if strategy == "left" else Atom.right_rep
        sample: List[Number] = []
        for atom, cnt in zip(self.atoms, totals):
            sample.extend([rep(atom)] * cnt)
        sample.sort()
        return sample


def _build_atoms(hists: Sequence[FixedHistogram]):
    bounds = sorted(set().union(*(set(h.boundaries) for h in hists)))
    edges = [NEG_INF] + bounds + [POS_INF]
    atoms = [Atom(edges[i], edges[i + 1]) for i in range(len(edges) - 1)]
    pos = {b: i for i, b in enumerate(bounds)}
    return atoms, pos


def _bucket_atom_range(pos, lo_edge, hi_edge):
    return range(pos[lo_edge] + 1, pos[hi_edge] + 1)


class UnionHistogram(_ConstraintView):
    """不同样本（不同固定边界）直方图的并集视图。"""

    @classmethod
    def union(cls, histograms: Sequence[FixedHistogram]) -> "UnionHistogram":
        hists = tuple(histograms)
        if not hists:
            raise ValueError("至少需要一个直方图")
        for h in hists:
            if not isinstance(h, FixedHistogram):
                raise TypeError("union 只接受 FixedHistogram")
        atoms, pos = _build_atoms(hists)
        m = len(atoms)
        var_atom: List[int] = []
        equalities: List[Tuple[Tuple[int, ...], int]] = []
        for s, h in enumerate(hists):
            base = s * m
            var_atom.extend(range(m))

            def var_range(atom_range):
                return tuple(base + k for k in atom_range)

            b0, bn = h.boundaries[0], h.boundaries[-1]
            equalities.append((var_range(range(0, pos[b0] + 1)), h.underflow))
            for i, c in enumerate(h.counts):
                equalities.append((var_range(_bucket_atom_range(
                    pos, h.boundaries[i], h.boundaries[i + 1])), c))
            equalities.append((var_range(range(pos[bn] + 1, m)), h.overflow))
        ch = cls()
        ch._init(atoms, var_atom, equalities,
                 sum(h.nulls for h in hists), sum(h.nans for h in hists),
                 sum(h.total for h in hists), hists)
        # 并集约束按构造必然可行（每个源可独立拆分），仍断言一次防御
        if _solve(len(var_atom), ch._constraints) is None:
            raise HistogramError("内部错误：并集约束不可行")
        return ch

    def __repr__(self) -> str:
        return (f"UnionHistogram(atoms={[str(a) for a in self.atoms]}, "
                f"sources={len(self.sources)}, total={self.total})")


class ReconciledHistogram(_ConstraintView):
    """同一样本被不同固定边界统计的多个直方图的调和视图。"""

    @classmethod
    def reconcile(cls, histograms: Sequence[FixedHistogram]) -> "ReconciledHistogram":
        hists = tuple(histograms)
        if not hists:
            raise ValueError("至少需要一个直方图")
        first = hists[0]
        for h in hists:
            if not isinstance(h, FixedHistogram):
                raise TypeError("reconcile 只接受 FixedHistogram")
            if h.total != first.total or h.nulls != first.nulls \
                    or h.nans != first.nans:
                raise IncompatibleHistograms(
                    "统计不相容：总数/null/NaN 计数不一致 "
                    f"({(h.total, h.nulls, h.nans)} != "
                    f"{(first.total, first.nulls, first.nans)})")
        atoms, pos = _build_atoms(hists)
        m = len(atoms)
        equalities: List[Tuple[Tuple[int, ...], int]] = []
        for h in hists:
            b0, bn = h.boundaries[0], h.boundaries[-1]
            equalities.append((tuple(range(0, pos[b0] + 1)), h.underflow))
            for i, c in enumerate(h.counts):
                equalities.append((tuple(_bucket_atom_range(
                    pos, h.boundaries[i], h.boundaries[i + 1])), c))
            equalities.append((tuple(range(pos[bn] + 1, m)), h.overflow))
        ch = cls()
        ch._init(atoms, list(range(m)), equalities,
                 first.nulls, first.nans, first.total, hists)
        if _solve(m, ch._constraints) is None:
            raise IncompatibleHistograms(
                "统计不相容：不存在满足全部桶约束的非负整数分布")
        return ch

    def __repr__(self) -> str:
        return (f"ReconciledHistogram(atoms={[str(a) for a in self.atoms]}, "
                f"sources={len(self.sources)}, total={self.total})")


def union(histograms: Sequence[FixedHistogram]) -> UnionHistogram:
    """合并不同样本、不同固定边界的直方图（拆分未知，约束表达）。"""
    return UnionHistogram.union(histograms)


def reconcile(histograms: Sequence[FixedHistogram]) -> ReconciledHistogram:
    """调和同一样本、不同固定边界的直方图（不相容则拒绝）。"""
    return ReconciledHistogram.reconcile(histograms)
