import { stronglyConnectedComponents, findCycleWithin } from './scc.ts';
import type { CycleWitness, PlanInput, PlanResult, PlannedBatch } from './types.ts';

export const MAX_PROJECTS = 18;

export class PlanInputError extends Error {}

/**
 * 多项目发布规划器。
 *
 * 约束语义：
 *  - 若 A 依赖 B 且二者不在同一依赖环上，则 B 必须在严格更早的批次发布；
 *  - 同一依赖环（强连通分量）上的项目必须同批发布（这是被允许且被强制的）；
 *  - 每批项目数不超过 capacity；
 *  - 目标：批次数最少。
 *
 * 算法：先求 SCC 缩点；若某环大小超过容量则直接返回不可行见证；
 * 否则在缩点后的 DAG 上做子集 DP（状态 = 已发布的 SCC 集合，
 * 转移 = 选一个“当前可发布且关于容量极大”的 SCC 子集作为下一批），
 * 极大性限制不丢最优解（交换论证：任何已就绪项目提前到当前批不会破坏可行性）。
 */
export function planRelease(input: PlanInput): PlanResult {
  const { names, prereqsOf } = validateAndBuild(input);
  const n = names.length;
  if (n === 0) return { ok: true, batchCount: 0, batches: [] };

  const components = stronglyConnectedComponents(n, prereqsOf);
  const sccOf = new Int32Array(n);
  components.forEach((members, id) => members.forEach((member) => (sccOf[member] = id)));

  // 可行性：必须同批发布的环不能超过容量。
  for (const members of components) {
    if (members.length > input.capacity) {
      return { ok: false, witness: buildWitness(members, prereqsOf, names, input.capacity) };
    }
  }

  const m = components.length;
  const weight = components.map((members) => members.length);
  const predMask = new Array<number>(m).fill(0);
  for (let u = 0; u < n; u++) {
    for (const v of prereqsOf[u]) {
      if (sccOf[v] !== sccOf[u]) predMask[sccOf[u]] |= 1 << sccOf[v];
    }
  }

  const batchOfScc = solveMinBatches(m, weight, predMask, input.capacity);
  // 不可行只会因为环超容量（上面已拦截），这里一定能解出。
  const batches = buildBatches(batchOfScc, components, prereqsOf, names, predMask, input.capacity);
  return { ok: true, batchCount: batches.length, batches };
}

function validateAndBuild(input: PlanInput): { names: string[]; prereqsOf: number[][] } {
  if (!Number.isInteger(input.capacity) || input.capacity < 1) {
    throw new PlanInputError(`容量上限必须是正整数，收到：${input.capacity}`);
  }
  if (!Array.isArray(input.projects)) {
    throw new PlanInputError('projects 必须是数组');
  }
  if (input.projects.length > MAX_PROJECTS) {
    throw new PlanInputError(`项目数最多 ${MAX_PROJECTS} 个，收到 ${input.projects.length} 个`);
  }
  const names = input.projects.map((p) => p.name);
  const indexOf = new Map<string, number>();
  names.forEach((name, i) => {
    if (typeof name !== 'string' || name.length === 0) {
      throw new PlanInputError(`第 ${i + 1} 个项目缺少合法名称`);
    }
    if (indexOf.has(name)) throw new PlanInputError(`项目名重复：${name}`);
    indexOf.set(name, i);
  });
  const prereqsOf: number[][] = names.map(() => []);
  input.projects.forEach((p, i) => {
    for (const dep of p.dependsOn ?? []) {
      if (dep === p.name) throw new PlanInputError(`项目不能依赖自身：${dep}`);
      const j = indexOf.get(dep);
      if (j === undefined) throw new PlanInputError(`项目 ${p.name} 依赖了未知项目：${dep}`);
      prereqsOf[i].push(j);
    }
  });
  return { names, prereqsOf };
}

function buildWitness(
  members: number[],
  prereqsOf: number[][],
  names: string[],
  capacity: number,
): CycleWitness {
  const cycle = findCycleWithin(members, prereqsOf).map((i) => names[i]);
  const memberNames = members.map((i) => names[i]);
  return {
    kind: 'oversized-cycle',
    members: memberNames,
    cycle,
    size: members.length,
    capacity,
    message:
      `依赖环 ${cycle.join(' → ')}（X → Y 表示 X 依赖 Y）上的 ${members.length} 个项目` +
      `必须同批发布，但每批容量上限为 ${capacity}，因此不存在可行发布计划。`,
  };
}

/**
 * 子集 DP 求最少批次。
 * 返回每个 SCC 所在的批次号（0 起始）；不可行时返回 null。
 */
function solveMinBatches(
  m: number,
  weight: number[],
  predMask: number[],
  capacity: number,
): Int32Array | null {
  const full = (1 << m) - 1;
  const dp = new Int32Array(1 << m).fill(-1);
  const choice = new Int32Array(1 << m).fill(-1);
  dp[0] = 0;

  // 缓存：给定“当前可发布集合”的位掩码，列出所有容量可行的极大子集。
  const maximalCache = new Map<number, number[]>();
  const maximalSubsets = (avail: number): number[] => {
    const cached = maximalCache.get(avail);
    if (cached) return cached;
    const items: number[] = [];
    let total = 0;
    for (let i = 0; i < m; i++) {
      if (avail & (1 << i)) {
        items.push(i);
        total += weight[i];
      }
    }
    let result: number[];
    if (total <= capacity) {
      result = [avail]; // 全部装得下时，唯一的极大子集就是全集
    } else {
      result = [];
      const visit = (pos: number, chosen: number, remaining: number): void => {
        if (pos === items.length) {
          // 极大性：任何未选项目都装不进剩余容量
          for (const item of items) {
            if (!(chosen & (1 << item)) && weight[item] <= remaining) return;
          }
          if (chosen !== 0) result.push(chosen);
          return;
        }
        const item = items[pos];
        if (weight[item] <= remaining) {
          visit(pos + 1, chosen | (1 << item), remaining - weight[item]);
        }
        visit(pos + 1, chosen, remaining);
      };
      visit(0, 0, capacity);
    }
    maximalCache.set(avail, result);
    return result;
  };

  for (let mask = 0; mask <= full; mask++) {
    const depth = dp[mask];
    if (depth < 0) continue;
    let avail = 0;
    for (let i = 0; i < m; i++) {
      if (!(mask & (1 << i)) && (predMask[i] & ~mask) === 0) avail |= 1 << i;
    }
    if (avail === 0) continue;
    for (const subset of maximalSubsets(avail)) {
      const next = mask | subset;
      if (dp[next] < 0 || dp[next] > depth + 1) {
        dp[next] = depth + 1;
        choice[next] = subset;
      }
    }
  }
  if (dp[full] < 0) return null;

  const batchOfScc = new Int32Array(m);
  let cursor = full;
  let batchIndex = dp[full] - 1;
  while (cursor !== 0) {
    const subset = choice[cursor];
    for (let i = 0; i < m; i++) {
      if (subset & (1 << i)) batchOfScc[i] = batchIndex;
    }
    cursor ^= subset;
    batchIndex--;
  }
  return batchOfScc;
}

function buildBatches(
  batchOfScc: Int32Array,
  components: number[][],
  prereqsOf: number[][],
  names: string[],
  predMask: number[],
  capacity: number,
): PlannedBatch[] {
  const m = components.length;
  const batchCount = m === 0 ? 0 : Math.max(...batchOfScc) + 1;

  // 每个 SCC 的“最早可发布批次”（仅由依赖链长度决定，不考虑容量）
  const earliest = new Array<number>(m).fill(1);
  const computeEarliest = (id: number): number => {
    if (earliest[id] !== 1 || predMask[id] === 0) return earliest[id];
    earliest[id] = 0; // 标记计算中（缩点图是 DAG，不会成环）
    for (let p = 0; p < m; p++) {
      if (predMask[id] & (1 << p)) {
        earliest[id] = Math.max(earliest[id], computeEarliest(p) + 1);
      }
    }
    return earliest[id];
  };
  for (let id = 0; id < m; id++) computeEarliest(id);

  const batches: PlannedBatch[] = [];
  for (let b = 0; b < batchCount; b++) {
    const sccIds: number[] = [];
    for (let id = 0; id < m; id++) if (batchOfScc[id] === b) sccIds.push(id);
    const projects = sccIds.flatMap((id) => components[id].map((member) => names[member])).sort();
    const reasons: string[] = [];
    for (const id of sccIds) {
      const memberNames = components[id].map((member) => names[member]);
      if (components[id].length > 1) {
        const cycle = findCycleWithin(components[id], prereqsOf).map((member) => names[member]);
        reasons.push(
          `${memberNames.join('、')} 构成依赖环（${cycle.join(' → ')}，X → Y 表示 X 依赖 Y），必须同批发布`,
        );
      }
      const preds: string[] = [];
      for (let p = 0; p < m; p++) {
        if (predMask[id] & (1 << p)) {
          const predNames = components[p].map((member) => names[member]).join('、');
          preds.push(`${predNames}（第 ${batchOfScc[p] + 1} 批）`);
        }
      }
      const label = memberNames.join('、');
      if (preds.length > 0) {
        reasons.push(`${label} 依赖 ${preds.join('、')}，须在其后发布`);
      } else {
        reasons.push(`${label} 无前置依赖`);
      }
      if (earliest[id] < b + 1) {
        reasons.push(`${label} 最早可于第 ${earliest[id]} 批发布，因批次容量限制顺延至本批`);
      }
    }
    reasons.push(
      `本批共 ${projects.length} 个项目（容量上限 ${capacity}），同批项目之间无未满足的先后约束；该分批使总批次数最少（共 ${batchCount} 批）`,
    );
    batches.push({ index: b + 1, projects, reasons });
  }
  return batches;
}
