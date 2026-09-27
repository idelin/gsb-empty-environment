#!/usr/bin/env python3.11
"""批量API成本预算器。

将一组作业划分为若干批次（打包），每个作业恰好属于一个批次，最小化总成本。

合法性约束：
  - 同一批次内所有作业必须属于同一租户（严格禁止跨租户混包）；
  - 同一批次内所有作业必须使用同一端点；
  - 同一批次内作业的载荷之和不得超过最大载荷上限。

成本模型：每包成本 = 固定费 + 载荷阶梯费（按包内总载荷落入的阶梯计费）。

求解方法：
  - 按 (租户, 端点) 分组后，组间互不可混包，总成本可加，故各组独立求解即全局最优；
  - 组内规模 <= exact_threshold 时用子集 bitmask DP 精确求解（可证最优）；
  - 组内规模超过阈值时退化为首次适应递减（FFD）启发式，结果未证最优，输出中会明确标注；
  - 总规模 <= verify_threshold 时，用独立的全划分枚举（restricted growth strings）
    交叉验证求解器结果。
"""
from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass
from itertools import groupby
from typing import Iterator, Sequence

COST_TOLERANCE = 1e-9


@dataclass(frozen=True)
class Job:
    id: str
    tenant: str
    endpoint: str
    payload: int


@dataclass(frozen=True)
class Tier:
    up_to: int
    fee: float


@dataclass(frozen=True)
class Config:
    max_payload: int
    fixed_fee: float
    tiers: tuple[Tier, ...]
    exact_threshold: int = 14
    verify_threshold: int = 10


@dataclass(frozen=True)
class Batch:
    jobs: tuple[Job, ...]

    @property
    def payload(self) -> int:
        return sum(j.payload for j in self.jobs)


@dataclass
class Solution:
    batches: list[Batch]
    total_cost: float
    optimal: bool
    notes: list[str]


def batch_cost(payload: int, config: Config) -> float:
    """单个非空批次的成本：固定费 + 按总载荷命中的第一个阶梯收费。

    若总载荷超过所有阶梯上限，按最高阶梯收费。
    """
    fee = config.fixed_fee
    for tier in config.tiers:
        if payload <= tier.up_to:
            return fee + tier.fee
    if config.tiers:
        return fee + config.tiers[-1].fee
    return fee


def parse_input(data: dict) -> tuple[Config, list[Job]]:
    raw_cfg = data.get("config", {})
    tiers = tuple(
        sorted(
            (Tier(up_to=int(t["up_to"]), fee=float(t["fee"])) for t in raw_cfg.get("payload_tiers", [])),
            key=lambda t: t.up_to,
        )
    )
    config = Config(
        max_payload=int(raw_cfg["max_payload"]),
        fixed_fee=float(raw_cfg.get("fixed_fee", 0)),
        tiers=tiers,
        exact_threshold=int(raw_cfg.get("exact_threshold", 14)),
        verify_threshold=int(raw_cfg.get("verify_threshold", 10)),
    )
    if config.max_payload <= 0:
        raise ValueError("max_payload 必须为正整数")
    jobs = []
    seen_ids: set[str] = set()
    for raw in data.get("jobs", []):
        job = Job(
            id=str(raw["id"]),
            tenant=str(raw["tenant"]),
            endpoint=str(raw["endpoint"]),
            payload=int(raw["payload"]),
        )
        if job.id in seen_ids:
            raise ValueError(f"作业 id 重复: {job.id}")
        seen_ids.add(job.id)
        if job.payload < 0:
            raise ValueError(f"作业 {job.id} 载荷为负")
        if job.payload > config.max_payload:
            raise ValueError(
                f"作业 {job.id} 载荷 {job.payload} 超过最大载荷 {config.max_payload}，无法合法打包"
            )
        jobs.append(job)
    return config, jobs


def group_key(job: Job) -> tuple[str, str]:
    return (job.tenant, job.endpoint)


def _solve_group_exact(jobs: Sequence[Job], config: Config) -> list[Batch]:
    """子集 bitmask DP，精确求解单组划分，保证组内最优。"""
    n = len(jobs)
    size = 1 << n
    payload_sum = [0] * size
    subset_cost: list[float | None] = [None] * size
    for mask in range(1, size):
        lsb = mask & (-mask)
        idx = lsb.bit_length() - 1
        total = payload_sum[mask ^ lsb] + jobs[idx].payload
        payload_sum[mask] = total
        if total <= config.max_payload:
            subset_cost[mask] = batch_cost(total, config)

    inf = float("inf")
    dp = [inf] * size
    choice = [0] * size
    dp[0] = 0.0
    for mask in range(1, size):
        lsb = mask & (-mask)
        sub = mask
        best = inf
        best_sub = 0
        while sub:
            if sub & lsb:
                cost = subset_cost[sub]
                if cost is not None:
                    candidate = dp[mask ^ sub] + cost
                    if candidate < best - COST_TOLERANCE:
                        best = candidate
                        best_sub = sub
            sub = (sub - 1) & mask
        dp[mask] = best
        choice[mask] = best_sub

    batches: list[Batch] = []
    mask = size - 1
    while mask:
        sub = choice[mask]
        batches.append(Batch(tuple(jobs[i] for i in range(n) if sub >> i & 1)))
        mask ^= sub
    return batches


def _solve_group_greedy(jobs: Sequence[Job], config: Config) -> list[Batch]:
    """首次适应递减（FFD）启发式，用于超出精确阈值的大组，未证最优。"""
    ordered = sorted(jobs, key=lambda j: j.payload, reverse=True)
    batches: list[list[Job]] = []
    remaining: list[int] = []
    for job in ordered:
        placed = False
        for i, batch in enumerate(batches):
            if remaining[i] >= job.payload:
                batch.append(job)
                remaining[i] -= job.payload
                placed = True
                break
        if not placed:
            batches.append([job])
            remaining.append(config.max_payload - job.payload)
    return [Batch(tuple(b)) for b in batches]


def solve(config: Config, jobs: Sequence[Job]) -> Solution:
    """按 (租户, 端点) 分组独立求解并合并。组间不可混包，故合并后仍保持各组的最优性状态。"""
    notes: list[str] = []
    all_batches: list[Batch] = []
    optimal = True
    sorted_jobs = sorted(jobs, key=group_key)
    for key, members_iter in groupby(sorted_jobs, key=group_key):
        members = list(members_iter)
        tenant, endpoint = key
        if len(members) <= config.exact_threshold:
            all_batches.extend(_solve_group_exact(members, config))
        else:
            optimal = False
            notes.append(
                f"组 (租户={tenant}, 端点={endpoint}) 含 {len(members)} 个作业，"
                f"超过精确阈值 {config.exact_threshold}，使用 FFD 启发式，该组结果未证最优"
            )
            all_batches.extend(_solve_group_greedy(members, config))
    total = sum(batch_cost(b.payload, config) for b in all_batches)
    return Solution(batches=all_batches, total_cost=total, optimal=optimal, notes=notes)


def _partitions(n: int) -> Iterator[list[int]]:
    """枚举 n 个元素的全部集合划分（restricted growth strings）。"""
    assignment = [0] * n

    def rec(i: int, max_block: int) -> Iterator[list[int]]:
        if i == n:
            yield list(assignment)
            return
        for block in range(max_block + 2):
            assignment[i] = block
            yield from rec(i + 1, max(max_block, block))

    yield from rec(0, -1)


def brute_force_min_cost(config: Config, jobs: Sequence[Job]) -> tuple[float, int]:
    """独立枚举全部集合划分，返回 (最小成本, 检查的划分总数)。用于小规模交叉验证。"""
    n = len(jobs)
    best = float("inf")
    checked = 0
    for assignment in _partitions(n):
        checked += 1
        blocks: dict[int, list[int]] = {}
        for idx, block in enumerate(assignment):
            blocks.setdefault(block, []).append(idx)
        total = 0.0
        legal = True
        for members in blocks.values():
            first = jobs[members[0]]
            payload = 0
            for idx in members:
                job = jobs[idx]
                if job.tenant != first.tenant or job.endpoint != first.endpoint:
                    legal = False
                    break
                payload += job.payload
            if not legal or payload > config.max_payload:
                legal = False
                break
            total += batch_cost(payload, config)
        if legal and total < best - COST_TOLERANCE:
            best = total
    return best, checked


def verify_solution(config: Config, jobs: Sequence[Job], solution: Solution) -> dict:
    """交叉验证：覆盖完整性校验 + （小规模时）独立枚举对照。"""
    report: dict = {"coverage_ok": True, "legality_ok": True, "problems": []}

    seen: list[str] = []
    for batch in solution.batches:
        if not batch.jobs:
            report["legality_ok"] = False
            report["problems"].append("存在空批次")
        tenant = batch.jobs[0].tenant
        endpoint = batch.jobs[0].endpoint
        for job in batch.jobs:
            seen.append(job.id)
            if job.tenant != tenant:
                report["legality_ok"] = False
                report["problems"].append(f"批次跨租户混包: {job.id}")
            if job.endpoint != endpoint:
                report["legality_ok"] = False
                report["problems"].append(f"批次跨端点混包: {job.id}")
        if batch.payload > config.max_payload:
            report["legality_ok"] = False
            report["problems"].append(f"批次载荷 {batch.payload} 超过上限 {config.max_payload}")
    if sorted(seen) != sorted(j.id for j in jobs) or len(seen) != len(set(seen)):
        report["coverage_ok"] = False
        report["problems"].append("作业未被恰好覆盖一次")

    if len(jobs) <= config.verify_threshold:
        enum_best, checked = brute_force_min_cost(config, jobs)
        report["enumeration"] = {
            "partitions_checked": checked,
            "min_cost": enum_best,
            "solver_cost": solution.total_cost,
            "consistent": abs(enum_best - solution.total_cost) <= COST_TOLERANCE,
        }
        if not report["enumeration"]["consistent"]:
            report["problems"].append(
                f"求解器成本 {solution.total_cost} 与枚举最小成本 {enum_best} 不一致"
            )
    else:
        report["enumeration"] = None
        report["problems"].append(
            f"作业数 {len(jobs)} 超过枚举阈值 {config.verify_threshold}，未做枚举对照"
        )
    return report


def solution_to_dict(solution: Solution, config: Config) -> dict:
    return {
        "optimal": solution.optimal,
        "total_cost": solution.total_cost,
        "num_batches": len(solution.batches),
        "notes": solution.notes,
        "batches": [
            {
                "jobs": [j.id for j in batch.jobs],
                "tenant": batch.jobs[0].tenant,
                "endpoint": batch.jobs[0].endpoint,
                "payload": batch.payload,
                "cost": batch_cost(batch.payload, config),
            }
            for batch in solution.batches
        ],
    }


def format_text(solution: Solution, config: Config, verification: dict | None) -> str:
    lines = []
    lines.append("=" * 56)
    lines.append(f"最优性: {'已证最优（子集DP精确求解）' if solution.optimal else '未证最优（含启发式分组）'}")
    for note in solution.notes:
        lines.append(f"  注意: {note}")
    lines.append(f"总成本: {solution.total_cost:g}    批次数: {len(solution.batches)}")
    lines.append("-" * 56)
    for i, batch in enumerate(solution.batches, 1):
        ids = ", ".join(j.id for j in batch.jobs)
        cost = batch_cost(batch.payload, config)
        lines.append(
            f"批次 {i}: 租户={batch.jobs[0].tenant} 端点={batch.jobs[0].endpoint} "
            f"载荷={batch.payload}/{config.max_payload} 成本={cost:g} 作业=[{ids}]"
        )
    if verification is not None:
        lines.append("-" * 56)
        lines.append(
            f"覆盖校验: {'通过（每个作业恰好一次）' if verification['coverage_ok'] else '失败'}"
        )
        lines.append(f"合法性校验: {'通过（无跨租户/跨端点/超载）' if verification['legality_ok'] else '失败'}")
        enum = verification.get("enumeration")
        if enum is None:
            lines.append("枚举对照: 未执行（规模超过枚举阈值，结果未独立验证）")
        else:
            status = "一致" if enum["consistent"] else "不一致！"
            lines.append(
                f"枚举对照: 独立枚举 {enum['partitions_checked']} 种划分，"
                f"最小成本 {enum['min_cost']:g}，求解器 {enum['solver_cost']:g} -> {status}"
            )
        for problem in verification["problems"]:
            lines.append(f"  问题: {problem}")
    lines.append("=" * 56)
    return "\n".join(lines)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="批量API成本预算器：求覆盖每个作业恰好一次的最低成本打包",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="输入为 JSON 文件（或 '-' 表示标准输入），格式见 README.md",
    )
    parser.add_argument("input", help="输入 JSON 文件路径，或 '-' 表示标准输入")
    parser.add_argument("--json", action="store_true", help="以 JSON 格式输出")
    parser.add_argument("--no-verify", action="store_true", help="跳过覆盖/合法性/枚举对照校验")
    args = parser.parse_args(argv)

    raw = sys.stdin.read() if args.input == "-" else open(args.input, encoding="utf-8").read()
    try:
        config, jobs = parse_input(json.loads(raw))
    except (KeyError, ValueError, TypeError) as exc:
        print(f"输入错误: {exc}", file=sys.stderr)
        return 2

    solution = solve(config, jobs)
    verification = None if args.no_verify else verify_solution(config, jobs, solution)

    if args.json:
        out = solution_to_dict(solution, config)
        if verification is not None:
            out["verification"] = verification
        print(json.dumps(out, ensure_ascii=False, indent=2))
    else:
        print(format_text(solution, config, verification))

    if verification is not None and (
        not verification["coverage_ok"]
        or not verification["legality_ok"]
        or (verification.get("enumeration") and not verification["enumeration"]["consistent"])
    ):
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
