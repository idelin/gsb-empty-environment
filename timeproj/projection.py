"""Exact projection of continuous time intervals onto sampled streams.

The projection of a continuous interval is in general *not* a contiguous
array: it is a set of index slices (one per run of available samples),
explicit gaps (regions with no coverage at all), dropped samples and
de-duplication evidence.  All arithmetic is exact (:class:`Fraction`), so
``[0, 1]`` at period 1 contains two samples while ``[0, 1)`` contains one,
and ceil/floor behave correctly for negative times.
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction

from .blocks import SamplingBlock
from .types import Duration, Interval, TimePoint, _nonempty

BlockKey = tuple  # (epoch, version)


@dataclass(frozen=True)
class IndexSlice:
    """A half-open run ``[start_index, end_index)`` of sample indices."""

    epoch: int
    version: int
    start_index: int  # inclusive
    end_index: int    # exclusive

    @property
    def key(self) -> BlockKey:
        return (self.epoch, self.version)

    def __len__(self) -> int:
        return self.end_index - self.start_index

    def indices(self):
        return range(self.start_index, self.end_index)


@dataclass(frozen=True)
class Gap:
    """A region of the query with no block coverage (never zero-filled)."""

    interval: Interval
    reason: str = "no-coverage"


@dataclass(frozen=True)
class MissingSample:
    """A sample the source dropped inside an otherwise covered region."""

    epoch: int
    version: int
    index: int
    timestamp: TimePoint


@dataclass(frozen=True)
class DedupeEvent:
    """Two blocks stamped the same instant; higher (epoch, version) won."""

    timestamp: TimePoint
    kept: tuple     # (epoch, version, index)
    dropped: tuple  # (epoch, version, index)


@dataclass(frozen=True)
class RoundingEvidence:
    """How one query endpoint was rounded onto the sample grid."""

    epoch: int
    version: int
    endpoint: str        # "start" | "end"
    requested: TimePoint
    raw_value: Fraction  # (requested - origin) / period
    exact_hit: bool      # requested time lands exactly on a sample
    direction: str       # "ceil" | "floor" | "floor+1" | "ceil-1"
    index: int | None
    timestamp: TimePoint | None


@dataclass(frozen=True)
class ReverseCoverage:
    """The interval a slice actually covers, plus its rounding evidence."""

    epoch: int
    version: int
    start_index: int
    end_index: int
    interval: Interval   # [first sample ts, last sample ts], both closed
    cell: Interval       # Voronoi extent [first - p/2, last + p/2)
    evidence: tuple      # RoundingEvidence of the forward projection


@dataclass
class ProjectionResult:
    """The honest shape of a projection: slices + gaps + evidence."""

    query: Interval
    slices: list                  # IndexSlice, sorted by first timestamp
    gaps: list                    # Gap
    missing: list                 # MissingSample
    dedupe: list                  # DedupeEvent
    evidence: list                # RoundingEvidence
    blocks: dict                  # BlockKey -> SamplingBlock

    @property
    def sample_count(self) -> int:
        return sum(len(s) for s in self.slices)

    def reverse(self) -> list:
        """Map every slice back to the interval it actually covers."""
        out = []
        for s in self.slices:
            block = self.blocks[s.key]
            half = Duration(block.period / 2)
            first = block.timestamp(s.start_index)
            last = block.timestamp(s.end_index - 1)
            cell = Interval(first - half, last + half, False, True)
            ev = tuple(e for e in self.evidence
                       if (e.epoch, e.version) == s.key)
            out.append(ReverseCoverage(
                s.epoch, s.version, s.start_index, s.end_index,
                Interval(first, last), cell, ev))
        return out


# --------------------------------------------------------------------------
# per-block projection

def _block_coverage(block: SamplingBlock, query: Interval) -> Interval | None:
    """The block's coverage clipped to the query, or None."""
    cov_end = block.end if block.end is not None else query.end
    cov_end_open = True if block.end is not None else query.end_open
    if not _nonempty(block.start.value, False, cov_end.value, cov_end_open):
        return None
    coverage = Interval(block.start, cov_end, False, cov_end_open)
    return query.intersection(coverage)


def _remove_index(runs: list, k: int) -> list:
    out = []
    for lo, hi in runs:
        if lo <= k < hi:
            if lo < k:
                out.append([lo, k])
            if k + 1 < hi:
                out.append([k + 1, hi])
        else:
            out.append([lo, hi])
    return out


def _block_runs(query: Interval, block: SamplingBlock):
    """Project ``query`` onto a single block.

    Returns ``(runs, missing, evidence, coverage)`` where ``runs`` is a list
    of half-open ``[lo, hi)`` index ranges with dropped samples cut out.
    """
    eff = _block_coverage(block, query)
    if eff is None:
        return [], [], [], None
    period = block.period
    x_lo = (eff.start.value - block.origin.value) / period
    if eff.start_open:
        k_lo, dir_lo = math.floor(x_lo) + 1, "floor+1"
    else:
        k_lo, dir_lo = math.ceil(x_lo), "ceil"
    x_hi = (eff.end.value - block.origin.value) / period
    if eff.end_open:
        k_hi, dir_hi = math.ceil(x_hi) - 1, "ceil-1"
    else:
        k_hi, dir_hi = math.floor(x_hi), "floor"
    hit_lo = x_lo.denominator == 1
    hit_hi = x_hi.denominator == 1
    evidence = []
    if k_lo <= k_hi:
        evidence.append(RoundingEvidence(
            block.epoch, block.version, "start", eff.start, x_lo,
            hit_lo, dir_lo, k_lo, block.timestamp(k_lo)))
        evidence.append(RoundingEvidence(
            block.epoch, block.version, "end", eff.end, x_hi,
            hit_hi, dir_hi, k_hi, block.timestamp(k_hi)))
    else:
        evidence.append(RoundingEvidence(
            block.epoch, block.version, "start", eff.start, x_lo,
            hit_lo, dir_lo, None, None))
        evidence.append(RoundingEvidence(
            block.epoch, block.version, "end", eff.end, x_hi,
            hit_hi, dir_hi, None, None))
    runs = [[k_lo, k_hi + 1]] if k_lo <= k_hi else []
    missing = []
    for m in sorted(block.missing):
        if k_lo <= m <= k_hi:
            missing.append(MissingSample(block.epoch, block.version, m,
                                         block.timestamp(m)))
            runs = _remove_index(runs, m)
    return runs, missing, evidence, eff


# --------------------------------------------------------------------------
# exact coincidence of two rational grids

def _egcd(a: int, b: int):
    old_r, r = a, b
    old_s, s = 1, 0
    old_t, t = 0, 1
    while r:
        q = old_r // r
        old_r, r = r, old_r - q * r
        old_s, s = s, old_s - q * s
        old_t, t = t, old_t - q * t
    return old_r, old_s, old_t  # g, x, y with a*x + b*y == g


def _ceil_div(a: int, b: int) -> int:  # b > 0
    return -((-a) // b)


def _coincidences(b1, lo1, hi1, b2, lo2, hi2):
    """Yield (k1, k2) with b1.timestamp(k1) == b2.timestamp(k2) inside the
    half-open index ranges [lo1, hi1) and [lo2, hi2).

    Solves k1*p1 - k2*p2 = origin2 - origin1 exactly over the integers.
    """
    p1, p2 = b1.period, b2.period
    delta = b2.origin.value - b1.origin.value
    denom = math.lcm(p1.denominator, p2.denominator)
    n1 = p1.numerator * (denom // p1.denominator)
    n2 = p2.numerator * (denom // p2.denominator)
    num = delta * denom
    if num.denominator != 1:
        return
    g, a, b = _egcd(n1, n2)
    if num.numerator % g != 0:
        return
    m = num.numerator // g
    k1_0, k2_0 = a * m, -b * m
    s1, s2 = n2 // g, n1 // g
    t_lo = max(_ceil_div(lo1 - k1_0, s1), _ceil_div(lo2 - k2_0, s2))
    t_hi = min((hi1 - 1 - k1_0) // s1, (hi2 - 1 - k2_0) // s2)
    for t in range(t_lo, t_hi + 1):
        yield k1_0 + t * s1, k2_0 + t * s2


def _in_runs(runs, k) -> bool:
    return any(lo <= k < hi for lo, hi in runs)


# --------------------------------------------------------------------------
# assembly

def _subtract_all(query: Interval, coverages: list) -> list:
    remaining = [query]
    for cov in sorted(coverages, key=lambda iv: (iv.start.value, iv.end.value)):
        nxt = []
        for r in remaining:
            nxt.extend(r.subtract(cov))
        remaining = nxt
        if not remaining:
            break
    return remaining


def assemble(query, blocks, runs, missing, evidence, coverages):
    """Build a ProjectionResult from per-block runs.

    Applies the de-duplication rule (higher ``(epoch, version)`` wins at
    identical timestamps), derives gaps and sorts deterministically.
    """
    runs = {key: [list(r) for r in rs] for key, rs in runs.items()}
    dedupe = []
    keys = sorted(runs)
    for i in range(len(keys)):
        for j in range(i + 1, len(keys)):
            k1, k2 = keys[i], keys[j]
            if not runs[k1] or not runs[k2]:
                continue
            b1, b2 = blocks[k1], blocks[k2]
            lo1 = min(lo for lo, _ in runs[k1])
            hi1 = max(hi for _, hi in runs[k1])
            lo2 = min(lo for lo, _ in runs[k2])
            hi2 = max(hi for _, hi in runs[k2])
            for c1, c2 in _coincidences(b1, lo1, hi1, b2, lo2, hi2):
                if not _in_runs(runs[k1], c1) or not _in_runs(runs[k2], c2):
                    continue
                if k1 > k2:
                    win_key, win_idx, lose_key, lose_idx = k1, c1, k2, c2
                else:
                    win_key, win_idx, lose_key, lose_idx = k2, c2, k1, c1
                runs[lose_key] = _remove_index(runs[lose_key], lose_idx)
                dedupe.append(DedupeEvent(
                    b1.timestamp(c1),
                    (win_key[0], win_key[1], win_idx),
                    (lose_key[0], lose_key[1], lose_idx)))
    slices = []
    for key in keys:
        for lo, hi in sorted(runs[key]):
            slices.append(IndexSlice(key[0], key[1], lo, hi))
    slices.sort(key=lambda s: (blocks[s.key].timestamp(s.start_index).value,
                               s.epoch, s.version, s.start_index))
    gaps = [Gap(iv) for iv in _subtract_all(query, coverages)]
    missing = sorted(missing, key=lambda m: (m.epoch, m.version, m.index))
    dedupe.sort(key=lambda d: (d.timestamp.value, d.kept))
    evidence = sorted(evidence, key=lambda e: (e.epoch, e.version, e.endpoint))
    return ProjectionResult(query, slices, gaps, missing, dedupe, evidence,
                            dict(blocks))


def _project_parts(query, blocks):
    blocks = {(b.epoch, b.version): b for b in blocks}
    runs, missing, evidence, coverages = {}, [], [], []
    for key, block in blocks.items():
        r, m, ev, cov = _block_runs(query, block)
        runs[key] = r
        missing.extend(m)
        evidence.extend(ev)
        if cov is not None:
            coverages.append(cov)
    return blocks, runs, missing, evidence, coverages


def project(query: Interval, blocks) -> ProjectionResult:
    """Project a continuous interval onto a set of sampling blocks."""
    blocks, runs, missing, evidence, coverages = _project_parts(query, blocks)
    return assemble(query, blocks, runs, missing, evidence, coverages)
