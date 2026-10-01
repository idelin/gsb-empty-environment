"""见证（witness）与动态变更后的重验。

求解结果可固化为 Witness；问题一旦被动态修改（如 delete_set），
version 递增，旧见证必须经 validate_witness 重验：
- 可行解见证：按当前问题重算可行性与收益；
- 最优性结论：问题变更后一律失效，需重新求解。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .model import Problem, Status


@dataclass
class Witness:
    problem_version: int
    selected_ids: tuple
    claimed_profit: Fraction
    claimed_status: Status
    source: str               # "greedy" / "exact" / "branch_bound"


@dataclass
class ValidationReport:
    ok: bool
    stale: bool               # 问题在见证生成后是否发生过变更
    problems: list
    recomputed_profit: Fraction | None


def make_witness(problem: Problem, result, source: str) -> Witness:
    if result.selected is None or result.profit is None:
        raise ValueError("无可行解的结果不能生成见证")
    return Witness(problem.version, tuple(result.selected), result.profit,
                   result.status, source)


def validate_witness(problem: Problem, witness: Witness) -> ValidationReport:
    problems: list[str] = []
    stale = witness.problem_version != problem.version
    recomputed = None
    known = {s.sid for s in problem.sets}
    missing = [sid for sid in witness.selected_ids if sid not in known]
    if missing:
        problems.append(f"见证引用的集合已不存在（可能被动态删除）: {missing}")
    else:
        selection = problem.selection_by_ids(witness.selected_ids)
        for msg in problem.violations(selection):
            problems.append(f"不可行: {msg}")
        recomputed = problem.profit(selection)
        if recomputed != witness.claimed_profit:
            problems.append(
                f"收益不符：见证声称 {witness.claimed_profit}，重算为 {recomputed}")
    if stale and witness.claimed_status is Status.OPTIMAL:
        problems.append(
            f"问题已变更（版本 {witness.problem_version} -> {problem.version}，"
            "如动态删集合），旧最优性结论失效，需重新求解")
    return ValidationReport(ok=not problems, stale=stale, problems=problems,
                            recomputed_profit=recomputed)
