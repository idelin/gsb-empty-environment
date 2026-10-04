import type {
  Assignment,
  Avoidance,
  Judge,
  SearchStats,
  SolveOptions,
  SolveResult,
  Work,
} from './types.ts';
import { findAvoidanceWitness, findCapacityWitness } from './witness.ts';

interface SearchBudget {
  nodesLeft: number;
  deadline: number;
}

interface SearchOutcome {
  /** 每份作品分配到的评委下标；未找到为 null */
  assignment: number[][] | null;
  nodes: number;
  aborted: boolean;
}

/**
 * 在负载界 [lo, hi] 与评委对联合限制（任意评委对共评 ≤ maxPairShared）下
 * 做完备 DFS（MRV 选作品 + 负载均衡排序 + 容量/缺口/评委对名额/可达性剪枝）。
 */
function searchAssignment(
  eligible: readonly (readonly number[])[],
  judgeCount: number,
  teamSize: number,
  maxPairShared: number,
  lo: number,
  hi: number,
  budget: SearchBudget,
): SearchOutcome {
  const workCount = eligible.length;
  const load = new Array<number>(judgeCount).fill(0);
  const pairCount = new Uint8Array(judgeCount * judgeCount);
  const assigned = new Array<boolean>(workCount).fill(false);
  const chosen = new Array<number[]>(workCount);
  const worksOfJudge: number[][] = Array.from({ length: judgeCount }, () => []);
  eligible.forEach((list, w) => {
    for (const j of list) worksOfJudge[j]!.push(w);
  });

  let nodes = 0;
  let aborted = false;

  const pairKey = (a: number, b: number): number => (a < b ? a * judgeCount + b : b * judgeCount + a);

  /** 作品 w 当前可行的评委组合（满足负载上界与评委对限制），最多生成 limit 个 */
  function feasibleTeams(w: number, limit: number): number[][] {
    const es = eligible[w]!;
    const out: number[][] = [];
    const acc: number[] = [];
    const gen = (start: number): void => {
      if (out.length >= limit) return;
      if (acc.length === teamSize) {
        out.push([...acc]);
        return;
      }
      const needed = teamSize - acc.length;
      for (let i = start; i <= es.length - needed; i++) {
        const j = es[i]!;
        if (load[j]! >= hi) continue;
        let ok = true;
        for (const other of acc) {
          if (pairCount[pairKey(other, j)]! >= maxPairShared) {
            ok = false;
            break;
          }
        }
        if (!ok) continue;
        acc.push(j);
        gen(i + 1);
        acc.pop();
        if (out.length >= limit) return;
      }
    };
    gen(0);
    return out;
  }

  function dfs(remaining: number): boolean {
    if (aborted) return false;
    nodes += 1;
    if (nodes > budget.nodesLeft || ((nodes & 1023) === 0 && Date.now() > budget.deadline)) {
      aborted = true;
      return false;
    }

    // 全局负载剪枝：剩余容量要够、达到下界的缺口要能补上
    const need = teamSize * remaining;
    let capacityLeft = 0;
    let deficit = 0;
    for (let j = 0; j < judgeCount; j++) {
      const lj = load[j]!;
      capacityLeft += hi - lj;
      if (lj < lo) deficit += lo - lj;
    }
    if (capacityLeft < need || deficit > need) return false;
    if (remaining === 0) return true; // deficit === 0 已保证所有负载 ≥ lo

    // 评委对名额剪枝：剩余评委对容量要覆盖剩余作品的联合需求
    let pairSlots = 0;
    for (let a = 0; a < judgeCount; a++) {
      for (let b = a + 1; b < judgeCount; b++) {
        pairSlots += maxPairShared - pairCount[a * judgeCount + b]!;
      }
    }
    if (pairSlots < ((teamSize * (teamSize - 1)) / 2) * remaining) return false;

    // 下界可达性：评委 j 最多还能从可选作品中获得 load 增量
    for (let j = 0; j < judgeCount; j++) {
      const lj = load[j]!;
      if (lj >= lo) continue;
      let reachable = lj;
      for (const w of worksOfJudge[j]!) {
        if (!assigned[w]!) reachable += 1;
      }
      if (reachable < lo) return false;
    }

    // MRV：选可行组合最少的作品（同时为前向检查）
    let bestW = -1;
    let bestTeams: number[][] | null = null;
    for (let w = 0; w < workCount; w++) {
      if (assigned[w]!) continue;
      const limit = bestTeams === null ? Number.POSITIVE_INFINITY : bestTeams.length;
      const teams = feasibleTeams(w, limit);
      if (teams.length === 0) return false;
      if (bestTeams === null || teams.length < bestTeams.length) {
        bestTeams = teams;
        bestW = w;
        if (teams.length === 1) break;
      }
    }

    // 均衡排序：优先补低负载评委（尤其未达下界者）
    const teams = bestTeams!;
    const belowLo = (team: number[]): number => {
      let c = 0;
      for (const j of team) if (load[j]! < lo) c += 1;
      return c;
    };
    const loadSum = (team: number[]): number => team.reduce((s, j) => s + load[j]!, 0);
    teams.sort((t1, t2) => belowLo(t2) - belowLo(t1) || loadSum(t1) - loadSum(t2));

    for (const team of teams) {
      for (const j of team) load[j]! += 1;
      for (let x = 0; x < team.length; x++) {
        for (let y = x + 1; y < team.length; y++) {
          pairCount[pairKey(team[x]!, team[y]!)]! += 1;
        }
      }
      assigned[bestW] = true;
      chosen[bestW] = team;
      const ok = dfs(remaining - 1);
      if (ok || aborted) return ok;
      assigned[bestW] = false;
      for (const j of team) load[j]! -= 1;
      for (let x = 0; x < team.length; x++) {
        for (let y = x + 1; y < team.length; y++) {
          pairCount[pairKey(team[x]!, team[y]!)]! -= 1;
        }
      }
    }
    return false;
  }

  const found = dfs(workCount);
  return { assignment: found ? chosen : null, nodes, aborted };
}

/**
 * 评委分配器主入口。
 *
 * 约束：每份作品恰好 teamSize 名评委；专业匹配；回避关系；
 * 任意评委对共评作品数 ≤ maxPairShared（联合限制，全局强制）。
 * 目标：最小化工作量最大差 max(load) - min(load)。
 *
 * 作品数超过 maxWorks（默认 12）时返回 error。
 * 无解时返回回避见证或容量见证；两者都不适用时返回穷举证明。
 */
export function solve(
  judges: readonly Judge[],
  works: readonly Work[],
  avoidances: readonly Avoidance[] = [],
  options: SolveOptions = {},
): SolveResult {
  const startedAt = Date.now();
  const teamSize = options.teamSize ?? 3;
  const maxPairShared = options.maxPairShared ?? 2;
  const maxWorks = options.maxWorks ?? 12;
  const nodeBudget = options.nodeBudget ?? 1_000_000;
  const timeLimitMs = options.timeLimitMs ?? 30_000;

  const stats: SearchStats = { nodes: 0, attempts: 0, elapsedMs: 0 };
  const finalStats = (): SearchStats => ({ ...stats, elapsedMs: Date.now() - startedAt });

  if (!Number.isInteger(teamSize) || teamSize < 2) {
    return { status: 'error', message: `teamSize 必须是 ≥2 的整数，收到 ${teamSize}` };
  }
  if (!Number.isInteger(maxPairShared) || maxPairShared < 1) {
    return { status: 'error', message: `maxPairShared 必须是 ≥1 的整数，收到 ${maxPairShared}` };
  }
  if (works.length > maxWorks) {
    return { status: 'error', message: `规模超限：共 ${works.length} 份作品，求解器上限为 ${maxWorks} 份` };
  }
  const judgeIds = judges.map((j) => j.id);
  const workIds = works.map((w) => w.id);
  if (new Set(judgeIds).size !== judges.length) return { status: 'error', message: '评委 id 存在重复' };
  if (new Set(workIds).size !== works.length) return { status: 'error', message: '作品 id 存在重复' };
  const judgeIdSet = new Set(judgeIds);
  const workIdSet = new Set(workIds);
  for (const a of avoidances) {
    if (!judgeIdSet.has(a.judgeId)) return { status: 'error', message: `回避关系引用了未知评委：${a.judgeId}` };
    if (!workIdSet.has(a.workId)) return { status: 'error', message: `回避关系引用了未知作品：${a.workId}` };
  }

  const judgeCount = judges.length;
  const workCount = works.length;

  // 专业匹配 + 回避过滤得到每份作品的可用评委
  const avoided = new Set(avoidances.map((a) => `${a.workId}\n${a.judgeId}`));
  const eligible: number[][] = works.map((w) => {
    const list: number[] = [];
    judges.forEach((j, idx) => {
      if (j.specialties.includes(w.specialty) && !avoided.has(`${w.id}\n${j.id}`)) list.push(idx);
    });
    return list;
  });

  // 无解见证（一）：回避导致可用评委不足
  const avoidanceWitness = findAvoidanceWitness(workIds, eligible, judgeIds, teamSize);
  if (avoidanceWitness) return { status: 'infeasible', witness: avoidanceWitness, stats: finalStats() };

  // 无解见证（二）：评委对联合限制下的容量缺陷（Hall 型，枚举 2^m 子集）
  const masks = eligible.map((list) => list.reduce((acc, j) => acc | (1n << BigInt(j)), 0n));
  const capacityWitness = findCapacityWitness(workIds, masks, judgeIds, teamSize, maxPairShared);
  if (capacityWitness) return { status: 'infeasible', witness: capacityWitness, stats: finalStats() };

  if (workCount === 0) {
    const loads: Record<string, number> = {};
    for (const id of judgeIds) loads[id] = 0;
    return { status: 'optimal', assignment: {}, loads, minLoad: 0, maxLoad: 0, spread: 0, stats: finalStats() };
  }

  // 评委 j 的负载上界：L*(teamSize-1) ≤ maxPairShared*(n-1)
  const total = teamSize * workCount;
  const hiMax = Math.min(workCount, Math.floor((maxPairShared * (judgeCount - 1)) / (teamSize - 1)));
  const deadline = startedAt + timeLimitMs;

  // 按最大差递增枚举负载界 [lo, hi]，第一个可行者即最优
  for (let spread = 0; spread <= hiMax; spread++) {
    const loMin = Math.max(0, Math.ceil(total / judgeCount) - spread);
    const loMax = Math.min(Math.floor(total / judgeCount), hiMax - spread);
    for (let lo = loMin; lo <= loMax; lo++) {
      const hi = lo + spread;
      stats.attempts += 1;
      const outcome = searchAssignment(eligible, judgeCount, teamSize, maxPairShared, lo, hi, {
        nodesLeft: nodeBudget - stats.nodes,
        deadline,
      });
      stats.nodes += outcome.nodes;
      if (outcome.aborted) {
        return {
          status: 'error',
          message: `搜索超出预算（已展开 ${stats.nodes} 节点），结果未知；可调大 nodeBudget / timeLimitMs`,
        };
      }
      if (outcome.assignment) {
        const assignment: Assignment = {};
        const loads: Record<string, number> = {};
        for (const id of judgeIds) loads[id] = 0;
        outcome.assignment.forEach((team, w) => {
          assignment[workIds[w]!] = team.map((j) => judgeIds[j]!);
          for (const j of team) loads[judgeIds[j]!]! += 1;
        });
        const loadValues = Object.values(loads);
        return {
          status: 'optimal',
          assignment,
          loads,
          minLoad: Math.min(...loadValues),
          maxLoad: Math.max(...loadValues),
          spread,
          stats: finalStats(),
        };
      }
    }
  }

  return {
    status: 'infeasible',
    witness: {
      kind: 'exhausted',
      detail: `已穷举最大差 0..${hiMax} 的全部负载界组合，在评委对联合限制（≤${maxPairShared}）下不存在可行分配`,
    },
    stats: finalStats(),
  };
}

/** 校验一份分配是否满足全部约束，返回违规描述列表（空数组 = 合法） */
export function validateAssignment(
  assignment: Record<string, readonly string[]>,
  judges: readonly Judge[],
  works: readonly Work[],
  avoidances: readonly Avoidance[] = [],
  options: Pick<SolveOptions, 'teamSize' | 'maxPairShared'> = {},
): string[] {
  const teamSize = options.teamSize ?? 3;
  const maxPairShared = options.maxPairShared ?? 2;
  const violations: string[] = [];
  const judgeById = new Map(judges.map((j) => [j.id, j]));
  const workIds = new Set(works.map((w) => w.id));
  const avoided = new Set(avoidances.map((a) => `${a.workId}\n${a.judgeId}`));
  const pairUse = new Map<string, number>();

  for (const w of works) {
    const team = assignment[w.id];
    if (!team) {
      violations.push(`作品 ${w.id} 未分配评委`);
      continue;
    }
    if (team.length !== teamSize) violations.push(`作品 ${w.id} 评委数为 ${team.length}，应为 ${teamSize}`);
    if (new Set(team).size !== team.length) violations.push(`作品 ${w.id} 的评委存在重复`);
    for (const jid of team) {
      const j = judgeById.get(jid);
      if (!j) {
        violations.push(`作品 ${w.id} 分配了未知评委 ${jid}`);
        continue;
      }
      if (!j.specialties.includes(w.specialty)) {
        violations.push(`评委 ${jid} 的专业与作品 ${w.id} 所需专业（${w.specialty}）不符`);
      }
      if (avoided.has(`${w.id}\n${jid}`)) violations.push(`评委 ${jid} 与作品 ${w.id} 存在回避关系`);
    }
    for (let x = 0; x < team.length; x++) {
      for (let y = x + 1; y < team.length; y++) {
        const a = team[x]!;
        const b = team[y]!;
        const key = a < b ? `${a}\n${b}` : `${b}\n${a}`;
        const count = (pairUse.get(key) ?? 0) + 1;
        pairUse.set(key, count);
        if (count > maxPairShared) {
          violations.push(`评委对 ${a} & ${b} 共评 ${count} 份作品，超过联合限制 ${maxPairShared}`);
        }
      }
    }
  }
  for (const wid of Object.keys(assignment)) {
    if (!workIds.has(wid)) violations.push(`分配中包含未知作品 ${wid}`);
  }
  return violations;
}

/** 统计每位评委的工作量 */
export function computeLoads(
  assignment: Record<string, readonly string[]>,
  judges: readonly Judge[],
): Record<string, number> {
  const loads: Record<string, number> = {};
  for (const j of judges) loads[j.id] = 0;
  for (const team of Object.values(assignment)) {
    for (const jid of team) {
      if (jid in loads) loads[jid]! += 1;
    }
  }
  return loads;
}
