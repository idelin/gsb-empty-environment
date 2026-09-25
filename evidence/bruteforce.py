"""穷举参照求解器（小模型对照）：枚举全部子集验证极大一致选择。

仅用于小规模测试对照，与 solver.solve 的团枚举结果必须一致。
"""
from __future__ import annotations

from itertools import combinations

from .solver import _assemble, _conflicts, SolveResult, Evidence, SOLVER_VERSION, _Tracer


def brute_force_cliques(facts, rules) -> list:
    ids = sorted(f.fact_id for f in facts)
    by_id = {f.fact_id: f for f in facts}

    def conflict_free(sub) -> bool:
        return all(not _conflicts(by_id[a], by_id[b], rules)
                   for a, b in combinations(sub, 2))

    maximal = []
    for size in range(1, len(ids) + 1):
        for sub in combinations(ids, size):
            if not conflict_free(sub):
                continue
            if any(conflict_free(sub + (other,)) for other in ids if other not in sub):
                continue
            maximal.append(sub)
    return sorted(maximal)


def brute_solve(facts, rules, observation: float) -> SolveResult:
    facts = list(facts)
    rules = list(rules)
    tracer = _Tracer()
    cliques = brute_force_cliques(facts, rules)
    segments, reasons, clique_meta = _assemble(
        facts, rules, observation, cliques, False, tracer)
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
        truncated=False,
        evidence=evidence,
        trace=tuple(tracer.steps),
        cliques=clique_meta,
    )
