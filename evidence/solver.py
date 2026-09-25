"""求解器：在观察时刻 T 选择一致证据集，输出全部极大可行时间段。

语义定义：
- 一致选择 = 可见事实的非空子集，两两不冲突（无可见互斥规则同时覆盖两者来源），
  且所有成员业务区间交集非空。
- 极大一致选择 = 不能再加入任何可见事实而保持一致的选择（兼容图的极大团）。
- 可行时间段 = 极大一致选择的共同区间之并的连通分量；每段附带分段覆盖，
  每个覆盖片至少给出一个一致选择（证据见证）。
- 被可见事实覆盖、但没有任何极大一致选择覆盖的区域：
  搜索完整 -> "infeasible"（不一致，附原因）；搜索被预算截断 -> "unknown"（明确保留未知段）。
- 结果不是所有候选区间的简单交集：互斥来源会产生多个互斥的极大选择，
  各自给出可行段。
"""
from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from itertools import combinations

from .model import EvidenceStore, FactVersion, MutexRuleVersion

SOLVER_VERSION = "evidence-solver/1.0.0"

FEASIBLE = "feasible"
INFEASIBLE = "infeasible"
UNKNOWN = "unknown"


# ---------------------------------------------------------------- 结果类型

@dataclass(frozen=True)
class Selection:
    fact_refs: tuple  # 排序后的 "fact_id@vN" 元组

    def to_list(self) -> list:
        return list(self.fact_refs)


@dataclass(frozen=True)
class CoverPiece:
    start: float
    end: float
    selection: Selection


@dataclass(frozen=True)
class Segment:
    segment_id: str  # 内容哈希派生的稳定 ID
    status: str      # feasible | infeasible | unknown
    start: float
    end: float
    cover: tuple = ()

    def to_dict(self) -> dict:
        return {
            "segment_id": self.segment_id,
            "status": self.status,
            "start": self.start,
            "end": self.end,
            "cover": [
                {"start": p.start, "end": p.end, "selection": p.selection.to_list()}
                for p in self.cover
            ],
        }


@dataclass(frozen=True)
class Reason:
    reason_id: str
    kind: str        # NO_VISIBLE_FACTS | EMPTY_COMMON_INTERVAL | SEARCH_TRUNCATED
    detail: str
    fact_refs: tuple = ()

    def to_dict(self) -> dict:
        return {
            "reason_id": self.reason_id,
            "kind": self.kind,
            "detail": self.detail,
            "fact_refs": list(self.fact_refs),
        }


@dataclass(frozen=True)
class Step:
    """求解步骤：稳定 step_id + 求解器版本，保证过程可审计、可重放。"""

    step_id: str
    solver_version: str
    action: str
    detail: str


@dataclass(frozen=True)
class Evidence:
    observation_time: float
    fact_refs: tuple
    rule_refs: tuple
    solver_version: str

    def to_dict(self) -> dict:
        return {
            "observation_time": self.observation_time,
            "fact_refs": list(self.fact_refs),
            "rule_refs": list(self.rule_refs),
            "solver_version": self.solver_version,
        }


@dataclass(frozen=True)
class SolveResult:
    observation_time: float
    segments: tuple
    reasons: tuple
    truncated: bool
    evidence: Evidence
    trace: tuple
    cliques: tuple  # 内部使用：(refs, lo, hi, non_empty)，不进入 digest
    solver_version: str = SOLVER_VERSION

    def to_dict(self) -> dict:
        return {
            "observation_time": self.observation_time,
            "solver_version": self.solver_version,
            "truncated": self.truncated,
            "segments": [s.to_dict() for s in self.segments],
            "reasons": [r.to_dict() for r in self.reasons],
            "evidence": self.evidence.to_dict(),
        }

    def digest(self) -> str:
        """结果的内容摘要：相同证据与观察时刻必然得到相同 digest（可重放）。"""
        blob = json.dumps(self.to_dict(), sort_keys=True).encode("utf-8")
        return hashlib.sha256(blob).hexdigest()

    def feasible_segments(self) -> list:
        return [s for s in self.segments if s.status == FEASIBLE]


class _Tracer:
    def __init__(self) -> None:
        self._seq = 0
        self.steps: list[Step] = []

    def step(self, action: str, detail: str) -> None:
        self._seq += 1
        self.steps.append(Step(f"step-{self._seq:04d}", SOLVER_VERSION, action, detail))


# ---------------------------------------------------------------- 工具函数

def _stable_id(prefix: str, *parts) -> str:
    blob = json.dumps(parts, sort_keys=True, default=str).encode("utf-8")
    return f"{prefix}-{hashlib.sha1(blob).hexdigest()[:12]}"


def _conflicts(a: FactVersion, b: FactVersion, rules) -> bool:
    if a.source == b.source:
        return False
    return any(a.source in r.sources and b.source in r.sources for r in rules)


def _merge_intervals(intervals) -> list:
    """合并半开区间；相邻（端点相接）区间属于同一连通分量，一并合并。"""
    out: list[list] = []
    for lo, hi in sorted(intervals):
        if lo >= hi:
            continue
        if out and lo <= out[-1][1]:
            out[-1][1] = max(out[-1][1], hi)
        else:
            out.append([lo, hi])
    return [(lo, hi) for lo, hi in out]


def _subtract(intervals, removed) -> list:
    result = []
    for lo, hi in intervals:
        cur = [(lo, hi)]
        for rlo, rhi in removed:
            nxt = []
            for a, b in cur:
                if rhi <= a or rlo >= b:
                    nxt.append((a, b))
                    continue
                if a < rlo:
                    nxt.append((a, rlo))
                if rhi < b:
                    nxt.append((rhi, b))
            cur = nxt
        result.extend(cur)
    return _merge_intervals(result)


# ---------------------------------------------------------------- 团枚举

def enumerate_maximal_cliques(facts, rules, budget=None):
    """Bron–Kerbosch（带枢轴）枚举兼容图的极大团，顺序确定。

    budget 为最大团数上限；达到上限即停止并置 truncated=True，
    未探索区域由调用方标记为 unknown。
    """
    ids = sorted(f.fact_id for f in facts)
    if not ids:
        return [], False
    by_id = {f.fact_id: f for f in facts}
    adj = {i: set() for i in ids}
    for a, b in combinations(ids, 2):
        if not _conflicts(by_id[a], by_id[b], rules):
            adj[a].add(b)
            adj[b].add(a)

    cliques: list[tuple] = []
    truncated = False

    def bk(r, p, x) -> None:
        nonlocal truncated
        if truncated:
            return
        if not p and not x:
            cliques.append(tuple(sorted(r)))
            if budget is not None and len(cliques) >= budget:
                truncated = True
            return
        union = p | x
        # 先排序再取 max，保证跨进程确定性（不受哈希随机化影响）
        pivot = max(sorted(union), key=lambda u: len(p & adj[u]))
        for v in sorted(p - adj[pivot]):
            if truncated:
                return
            bk(r | {v}, p & adj[v], x & adj[v])
            p = p - {v}
            x = x | {v}

    bk(frozenset(), set(ids), set())
    cliques.sort()
    return cliques, truncated


# ---------------------------------------------------------------- 段装配

def _cover_for_component(comp, clique_intervals) -> tuple:
    lo, hi = comp
    inside = [
        (max(l, lo), min(h, hi), sel)
        for l, h, sel in clique_intervals
        if l < hi and lo < h
    ]
    points = sorted({p for iv in inside for p in iv[:2]})
    pieces: list[CoverPiece] = []
    for a, b in zip(points, points[1:]):
        if a >= b:
            continue
        active = sorted({sel for l, h, sel in inside if l <= a and b <= h})
        if not active:
            continue
        sel = active[0]
        if pieces and pieces[-1].selection.fact_refs == sel and pieces[-1].end == a:
            pieces[-1] = CoverPiece(pieces[-1].start, b, pieces[-1].selection)
        else:
            pieces.append(CoverPiece(a, b, Selection(sel)))
    return tuple(pieces)


def _assemble(facts, rules, observation, cliques, truncated, tracer):
    by_id = {f.fact_id: f for f in facts}
    reasons: list[Reason] = []
    if not facts:
        reasons.append(Reason(
            _stable_id("rsn", "NO_VISIBLE_FACTS", observation),
            "NO_VISIBLE_FACTS",
            f"观察时刻 {observation} 没有可见事实（晚于该时刻录入的证据不参与历史解释）",
        ))
        tracer.step("reason", "no visible facts")
    clique_intervals: list[tuple] = []  # (lo, hi, refs)
    clique_meta: list[tuple] = []

    for clique in cliques:
        members = [by_id[i] for i in clique]
        refs = tuple(m.ref for m in members)
        lo = max(m.valid_from for m in members)
        hi = min(m.valid_to for m in members)
        non_empty = lo < hi
        clique_meta.append((refs, lo, hi, non_empty))
        tracer.step("clique", f"{'{'}{', '.join(refs)}{'}'} -> [{lo}, {hi})")
        if non_empty:
            clique_intervals.append((lo, hi, refs))
        else:
            max_from = max(members, key=lambda m: (m.valid_from, m.ref))
            min_to = min(members, key=lambda m: (m.valid_to, m.ref))
            detail = (
                f"共同区间为空: max(valid_from)={lo} ({max_from.ref}) "
                f">= min(valid_to)={hi} ({min_to.ref})"
            )
            reasons.append(Reason(
                _stable_id("rsn", "EMPTY_COMMON_INTERVAL", refs, lo, hi),
                "EMPTY_COMMON_INTERVAL", detail, refs,
            ))
            tracer.step("reason", detail)

    feasible = _merge_intervals([(l, h) for l, h, _ in clique_intervals])
    covered = _merge_intervals([(f.valid_from, f.valid_to) for f in facts])
    rest = _subtract(covered, feasible)

    segments: list[Segment] = []
    for lo, hi in feasible:
        cover = _cover_for_component((lo, hi), clique_intervals)
        seg = Segment(_stable_id("seg", FEASIBLE, lo, hi,
                                 [(p.start, p.end, p.selection.fact_refs) for p in cover]),
                      FEASIBLE, lo, hi, cover)
        segments.append(seg)
        tracer.step("segment", f"{seg.segment_id} feasible [{lo}, {hi})")

    rest_status = UNKNOWN if truncated else INFEASIBLE
    for lo, hi in rest:
        seg = Segment(_stable_id("seg", rest_status, lo, hi), rest_status, lo, hi)
        segments.append(seg)
        tracer.step("segment", f"{seg.segment_id} {rest_status} [{lo}, {hi})")

    if truncated:
        reasons.append(Reason(
            _stable_id("rsn", "SEARCH_TRUNCATED", observation),
            "SEARCH_TRUNCATED",
            "团枚举达到预算上限，未探索区域已标记为 unknown 段",
        ))
        tracer.step("reason", "search truncated by budget")

    segments.sort(key=lambda s: (s.start, s.end, s.status))
    return tuple(segments), tuple(reasons), tuple(clique_meta)


# ---------------------------------------------------------------- 入口

def solve(facts, rules, observation: float, budget=None) -> SolveResult:
    tracer = _Tracer()
    facts = list(facts)
    rules = list(rules)

    cliques, truncated = enumerate_maximal_cliques(facts, rules, budget)
    segments, reasons, clique_meta = _assemble(
        facts, rules, observation, cliques, truncated, tracer)

    evidence = Evidence(
        observation,
        tuple(f.ref for f in facts),
        tuple(r.ref for r in rules),
        SOLVER_VERSION,
    )
    return SolveResult(
        observation_time=observation,
        segments=segments,
        reasons=reasons,
        truncated=truncated,
        evidence=evidence,
        trace=tuple(tracer.steps),
        cliques=clique_meta,
    )


def solve_store(store: EvidenceStore, observation: float, budget=None) -> SolveResult:
    return solve(store.visible_facts(observation),
                 store.visible_rules(observation),
                 observation, budget=budget)
