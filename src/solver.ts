import { BudgetExceeded, Normalized, SearchEnv, combinations, normalize, search } from './internal';
import { OkResult, Problem, SolveResult, Witness } from './types';
import { findCapacityWitness, findCombinatorialCore } from './witness';

function loadsOfSolution(sol: number[][], judgeIds: string[]): Record<string, number> {
  const loads: Record<string, number> = {};
  for (const id of judgeIds) loads[id] = 0;
  for (const combo of sol) for (const j of combo) loads[judgeIds[j]]++;
  return loads;
}

function diffOf(sol: number[][], J: number): number {
  const load = new Array<number>(J).fill(0);
  for (const combo of sol) for (const j of combo) load[j]++;
  let mn = Infinity;
  let mx = -Infinity;
  for (const l of load) {
    if (l < mn) mn = l;
    if (l > mx) mx = l;
  }
  return mx - mn;
}

function buildOk(norm: Normalized, sol: number[][], nodes: number): OkResult {
  const { workIds, judgeIds } = norm;
  const assignment: Record<string, string[]> = {};
  const pairCoJudging: Record<string, number> = {};
  sol.forEach((combo, w) => {
    const ids = combo.map((j) => judgeIds[j]);
    assignment[workIds[w]] = ids;
    for (let a = 0; a < ids.length; a++) {
      for (let b = a + 1; b < ids.length; b++) {
        const key = [ids[a], ids[b]].sort().join('|');
        pairCoJudging[key] = (pairCoJudging[key] ?? 0) + 1;
      }
    }
  });
  const loads = loadsOfSolution(sol, judgeIds);
  const vals = Object.values(loads);
  return {
    status: 'ok',
    assignment,
    loads,
    maxLoadDiff: vals.length ? Math.max(...vals) - Math.min(...vals) : 0,
    pairCoJudging,
    nodesExplored: nodes,
  };
}

/**
 * 求解评委分配：
 *  - 每份作品恰好 K 名评委（默认 3），专业匹配且不存在回避关系；
 *  - 任意评委对共同评审不超过 P 份作品（默认 2，联合限制不可忽略）；
 *  - 目标：最小化全体评委工作量的最大差。
 *
 * 无解时返回容量见证或回避见证；超出规模上限返回 unsupported；
 * 搜索预算耗尽（理论上可能）返回 undetermined。
 */
export function solve(problem: Problem): SolveResult {
  const norm = normalize(problem);
  const { K, P, workIds, judgeIds } = norm;
  const W = workIds.length;
  const J = judgeIds.length;

  if (W > norm.maxWorks) {
    return {
      status: 'unsupported',
      reason: `作品数 ${W} 超过规模上限 ${norm.maxWorks}`,
      workCount: W,
      maxWorks: norm.maxWorks,
    };
  }
  if (W === 0) {
    const loads: Record<string, number> = {};
    for (const id of judgeIds) loads[id] = 0;
    return { status: 'ok', assignment: {}, loads, maxLoadDiff: 0, pairCoJudging: {}, nodesExplored: 0 };
  }

  // 1) 回避见证：单份作品合格评委不足 K 名。
  for (let w = 0; w < W; w++) {
    if (norm.candidates[w].length < K) {
      const fieldMatch: number[] = [];
      for (let j = 0; j < J; j++) {
        if (norm.judgeFields[j].includes(norm.workFields[w])) fieldMatch.push(j);
      }
      const blocked = fieldMatch.filter((j) => norm.conflicted[j][w]).map((j) => judgeIds[j]);
      const witness: Witness = {
        type: 'work-conflict',
        workId: workIds[w],
        eligibleJudgeIds: norm.candidates[w].map((j) => judgeIds[j]),
        blockedByConflict: blocked,
        required: K,
        detail:
          `作品 ${workIds[w]}（专业 ${norm.workFields[w]}）合格评委仅 ${norm.candidates[w].length} 名，` +
          `不足 ${K} 名；${fieldMatch.length} 名同专业评委中有 ${blocked.length} 名因回避关系被排除`,
      };
      return { status: 'infeasible', witness };
    }
  }

  // 2) 容量见证：作品子集在评委对联合限制下的必要条件被违反。
  const capWitness = findCapacityWitness(norm);
  if (capWitness) return { status: 'infeasible', witness: capWitness };

  const allCombos = norm.candidates.map((c) => combinations(c, K));
  let totalNodes = 0;
  const run = (lo: number, hi: number): number[][] | null => {
    const env: SearchEnv = { J, K, P, nodeLimit: norm.nodeLimit, nodes: { count: 0 } };
    try {
      return search(allCombos, env, lo, hi);
    } finally {
      totalNodes += env.nodes.count;
    }
  };

  // 3) 先判定可行性（不加工作量约束，仅评委对联合限制）。
  let first: number[][] | null;
  try {
    first = run(0, W);
  } catch (e) {
    if (e instanceof BudgetExceeded) return { status: 'undetermined', reason: '可行性搜索超出节点预算，无法判定' };
    throw e;
  }

  if (first === null) {
    // 4) 组合核心：极小不可行作品子集作为严格见证。
    try {
      const core = findCombinatorialCore(norm, allCombos, (n) => (totalNodes += n));
      const judgeSet = new Set<number>();
      for (const w of core) for (const j of norm.candidates[w]) judgeSet.add(j);
      const witness: Witness = {
        type: 'combinatorial-core',
        workIds: core.map((w) => workIds[w]),
        judgeIds: [...judgeSet].map((j) => judgeIds[j]),
        detail:
          `作品子集 {${core.map((w) => workIds[w]).join(', ')}}（${core.length} 份）在评委对联合限制` +
          `（任意评委对最多共评 ${P} 份）下不可行：经完备搜索验证，且删去其中任意一份后即可行`,
      };
      return { status: 'infeasible', witness };
    } catch (e) {
      if (e instanceof BudgetExceeded) return { status: 'undetermined', reason: '见证构造超出节点预算，无法判定' };
      throw e;
    }
  }

  // 5) 最小化工作量最大差：总工作量固定为 K*W，若存在全部负载落在
  //    [lo, lo+D] 的解，则必有 ceil(avg)-D <= lo <= floor(avg)。
  //    按 D 升序枚举，首个可行 D 即最优。
  const total = K * W;
  const avg = total / J;
  let best = first;
  let bestDiff = diffOf(first, J);
  for (let D = 0; D < bestDiff; D++) {
    let found: number[][] | null = null;
    const loMax = Math.floor(avg);
    const loMin = Math.max(0, Math.ceil(avg - D));
    for (let lo = loMax; lo >= loMin; lo--) {
      try {
        found = run(lo, Math.min(lo + D, W));
      } catch (e) {
        if (e instanceof BudgetExceeded) return { status: 'undetermined', reason: '优化搜索超出节点预算，无法判定' };
        throw e;
      }
      if (found) break;
    }
    if (found) {
      best = found;
      bestDiff = D;
      break;
    }
  }

  return buildOk(norm, best, totalNodes);
}
