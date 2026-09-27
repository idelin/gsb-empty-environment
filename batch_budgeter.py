"""批量 API 成本预算器。

将有限作业按合法分组打包，求覆盖每个作业恰好一次的最低成本方案。

合法性约束：
  * 同一包内所有作业必须属于同一租户（严格禁止跨租户混包）；
  * 同一包内所有作业必须使用同一端点；
  * 同一包内载荷之和不得超过最大载荷 max_payload。

成本模型：每包成本 = 固定费 fixed_fee + 载荷阶梯费 tier_fee(包内总载荷)。
阶梯费由 payload_tiers 描述：[[上限, 费用], ...]，按上限升序，
总载荷落入第一个 上限 >= 总载荷 的档位即收取对应费用；空列表表示无变动费。

求解：按 (租户, 端点) 分解为独立子问题，每个子问题用子集 DP 求精确最优；
另提供独立的暴力枚举（受限增长串枚举全部划分）用于小规模对照验证。
"""

from __future__ import annotations

import argparse
import json
import math
import sys
from dataclasses import dataclass, field

# 子集 DP 的复杂度约为 O(3^n)，n 为同一 (租户, 端点) 类中的作业数。
# 超过该阈值时拒绝精确求解，避免指数爆炸。
MAX_EXACT_CLASS_SIZE = 22
# 暴力枚举划分数为贝尔数，仅用于小规模对照。
MAX_BRUTE_FORCE_JOBS = 10


@dataclass(frozen=True)
class Job:
    job_id: str
    tenant: str
    endpoint: str
    payload: int


@dataclass(frozen=True)
class CostModel:
    max_payload: int
    fixed_fee: float
    payload_tiers: tuple[tuple[int, float], ...] = field(default_factory=tuple)

    def tier_fee(self, payload: int) -> float:
        for up_to, fee in self.payload_tiers:
            if payload <= up_to:
                return fee
        if self.payload_tiers:
            raise ValueError(
                f"载荷 {payload} 超出阶梯表最大上限 {self.payload_tiers[-1][0]}"
            )
        return 0.0

    def package_cost(self, payload: int) -> float:
        return self.fixed_fee + self.tier_fee(payload)


@dataclass
class Package:
    jobs: list[Job]
    payload: int
    fixed_fee: float
    tier_fee: float
    cost: float


@dataclass
class Solution:
    packages: list[Package]
    total_cost: float


def parse_problem(data: dict) -> tuple[list[Job], CostModel]:
    try:
        max_payload = int(data["max_payload"])
        fixed_fee = float(data["fixed_fee"])
    except (KeyError, TypeError, ValueError) as exc:
        raise ValueError(f"输入缺少合法的 max_payload / fixed_fee: {exc}") from exc
    if max_payload <= 0:
        raise ValueError("max_payload 必须为正整数")
    if fixed_fee < 0:
        raise ValueError("fixed_fee 不得为负")

    raw_tiers = data.get("payload_tiers", [])
    tiers: list[tuple[int, float]] = []
    for entry in raw_tiers:
        up_to, fee = int(entry[0]), float(entry[1])
        if up_to <= 0 or fee < 0:
            raise ValueError(f"非法阶梯档位: {entry!r}")
        tiers.append((up_to, fee))
    tiers.sort()
    for prev, curr in zip(tiers, tiers[1:]):
        if prev[0] == curr[0]:
            raise ValueError(f"阶梯上限重复: {prev[0]}")

    jobs: list[Job] = []
    seen_ids: set[str] = set()
    for raw in data.get("jobs", []):
        job = Job(
            job_id=str(raw["id"]),
            tenant=str(raw["tenant"]),
            endpoint=str(raw["endpoint"]),
            payload=int(raw["payload"]),
        )
        if job.job_id in seen_ids:
            raise ValueError(f"作业 id 重复: {job.job_id}")
        seen_ids.add(job.job_id)
        if job.payload < 0:
            raise ValueError(f"作业 {job.job_id} 载荷为负")
        if job.payload > max_payload:
            raise ValueError(
                f"作业 {job.job_id} 载荷 {job.payload} 超过 max_payload {max_payload}，"
                "单独成包也不合法，问题无可行解"
            )
        jobs.append(job)
    return jobs, CostModel(max_payload, fixed_fee, tuple(tiers))


def _solve_class_exact(jobs: list[Job], model: CostModel) -> list[list[int]]:
    """对同一 (租户, 端点) 类用子集 DP 求最小成本划分，返回下标分组。"""
    n = len(jobs)
    if n > MAX_EXACT_CLASS_SIZE:
        raise ValueError(
            f"兼容类含 {n} 个作业，超过精确求解上限 {MAX_EXACT_CLASS_SIZE}"
        )
    payloads = [j.payload for j in jobs]
    size = 1 << n
    sum_payload = [0] * size
    legal = [False] * size
    for mask in range(1, size):
        lsb = mask & -mask
        bit = lsb.bit_length() - 1
        sum_payload[mask] = sum_payload[mask ^ lsb] + payloads[bit]
        legal[mask] = sum_payload[mask] <= model.max_payload
    legal[0] = True

    cost_cache: dict[int, float] = {}

    def mask_cost(mask: int) -> float:
        if mask not in cost_cache:
            cost_cache[mask] = model.package_cost(sum_payload[mask])
        return cost_cache[mask]

    inf = math.inf
    dp = [inf] * size
    choice = [0] * size
    dp[0] = 0.0
    full = size - 1
    for mask in range(size):
        if dp[mask] == inf:
            continue
        remaining = full ^ mask
        if not remaining:
            continue
        anchor = remaining & -remaining  # 固定最低未覆盖位，避免重复枚举
        sub = remaining
        while sub:
            if sub & anchor and legal[sub]:
                cand = dp[mask] + mask_cost(sub)
                nxt = mask | sub
                if cand < dp[nxt]:
                    dp[nxt] = cand
                    choice[nxt] = sub
            sub = (sub - 1) & remaining

    groups: list[list[int]] = []
    mask = full
    while mask:
        sub = choice[mask]
        groups.append([i for i in range(n) if sub >> i & 1])
        mask ^= sub
    return groups


def solve(jobs: list[Job], model: CostModel) -> Solution:
    """求覆盖每个作业恰好一次的最低成本打包。"""
    classes: dict[tuple[str, str], list[int]] = {}
    for idx, job in enumerate(jobs):
        classes.setdefault((job.tenant, job.endpoint), []).append(idx)

    packages: list[Package] = []
    for key in sorted(classes):
        class_jobs = [jobs[i] for i in classes[key]]
        for group in _solve_class_exact(class_jobs, model):
            members = [class_jobs[i] for i in group]
            payload = sum(j.payload for j in members)
            tier = model.tier_fee(payload)
            packages.append(
                Package(
                    jobs=members,
                    payload=payload,
                    fixed_fee=model.fixed_fee,
                    tier_fee=tier,
                    cost=model.fixed_fee + tier,
                )
            )
    packages.sort(key=lambda p: (p.jobs[0].tenant, p.jobs[0].endpoint, p.jobs[0].job_id))
    return Solution(packages=packages, total_cost=sum(p.cost for p in packages))


def brute_force_best(jobs: list[Job], model: CostModel) -> float:
    """独立实现：枚举全部集合划分（受限增长串），返回最低成本。

    与 solve() 的 DP 实现互不共享代码，用于小规模对照验证。
    """
    n = len(jobs)
    if n > MAX_BRUTE_FORCE_JOBS:
        raise ValueError(f"作业数 {n} 超过暴力枚举上限 {MAX_BRUTE_FORCE_JOBS}")
    if n == 0:
        return 0.0

    def compatible(group: list[int]) -> bool:
        first = jobs[group[0]]
        return (
            all(
                jobs[i].tenant == first.tenant and jobs[i].endpoint == first.endpoint
                for i in group
            )
            and sum(jobs[i].payload for i in group) <= model.max_payload
        )

    best = math.inf
    assignment = [0] * n

    def visit(pos: int, num_blocks: int) -> None:
        nonlocal best
        if pos == n:
            blocks: dict[int, list[int]] = {}
            for i, b in enumerate(assignment):
                blocks.setdefault(b, []).append(i)
            total = 0.0
            for group in blocks.values():
                if not compatible(group):
                    return
                total += model.package_cost(sum(jobs[i].payload for i in group))
            if total < best:
                best = total
            return
        # 受限增长串：第 pos 个元素只能加入已有块或新开一个块，无重复枚举。
        for b in range(num_blocks + 1):
            assignment[pos] = b
            visit(pos + 1, max(num_blocks, b + 1))

    visit(1, 1)
    return best


def verify_cover_exactly_once(jobs: list[Job], solution: Solution) -> None:
    counts: dict[str, int] = {}
    for pkg in solution.packages:
        for job in pkg.jobs:
            counts[job.job_id] = counts.get(job.job_id, 0) + 1
    expected = {j.job_id for j in jobs}
    if set(counts) != expected or any(v != 1 for v in counts.values()):
        raise AssertionError("打包结果未恰好覆盖每个作业一次")


def solution_to_dict(solution: Solution) -> dict:
    return {
        "total_cost": solution.total_cost,
        "package_count": len(solution.packages),
        "packages": [
            {
                "jobs": [j.job_id for j in pkg.jobs],
                "tenant": pkg.jobs[0].tenant,
                "endpoint": pkg.jobs[0].endpoint,
                "payload": pkg.payload,
                "fixed_fee": pkg.fixed_fee,
                "tier_fee": pkg.tier_fee,
                "cost": pkg.cost,
            }
            for pkg in solution.packages
        ],
    }


def run(data: dict, verify: bool = True) -> dict:
    jobs, model = parse_problem(data)
    solution = solve(jobs, model)
    verify_cover_exactly_once(jobs, solution)

    result = solution_to_dict(solution)
    verification: dict
    if not verify:
        verification = {"status": "skipped", "reason": "用户通过 --no-verify 关闭"}
    elif len(jobs) > MAX_BRUTE_FORCE_JOBS:
        verification = {
            "status": "skipped",
            "reason": f"作业数 {len(jobs)} 超过暴力枚举上限 {MAX_BRUTE_FORCE_JOBS}，"
            "最优性未经独立枚举验证",
        }
    else:
        brute = brute_force_best(jobs, model)
        match = math.isclose(brute, solution.total_cost, rel_tol=0, abs_tol=1e-9)
        verification = {
            "status": "confirmed" if match else "mismatch",
            "dp_total_cost": solution.total_cost,
            "brute_force_total_cost": brute,
        }
        if not match:
            raise AssertionError(
                f"DP 结果 {solution.total_cost} 与暴力枚举 {brute} 不一致"
            )
    result["verification"] = verification
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="batch_budgeter",
        description="批量 API 成本预算器：求覆盖每个作业恰好一次的最低成本打包。",
    )
    parser.add_argument("input", help="输入 JSON 文件路径，'-' 表示标准输入")
    parser.add_argument(
        "--no-verify",
        action="store_true",
        help="跳过小规模暴力枚举对照验证",
    )
    args = parser.parse_args(argv)

    try:
        text = sys.stdin.read() if args.input == "-" else open(args.input, encoding="utf-8").read()
        data = json.loads(text)
        result = run(data, verify=not args.no_verify)
    except (ValueError, KeyError, json.JSONDecodeError) as exc:
        print(f"错误: {exc}", file=sys.stderr)
        return 2
    json.dump(result, sys.stdout, ensure_ascii=False, indent=2)
    print()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
