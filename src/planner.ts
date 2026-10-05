// 多项目发布规划器
//
// 问题建模：
//   - 项目 A 依赖项目 B（A.deps 含 B）=> B 必须在严格更早的批次发布。
//     即「依赖方同步发布要求」：发布 A 时其依赖必须已经就绪。
//   - 依赖环（强连通分量）允许同批发布：环上项目互相依赖，无法严格
//     排序，唯一可行方式是把它们放进同一批，因此分量绝不能拆到两批。
//   - 每批项目数不超过容量 capacity，目标是最小化批次数。
//
// 不可行的唯一情形：某个依赖环的大小超过每批容量。此时输出见证：
// 环所在的强连通分量 + 一条具体的依赖环路径。

import { tarjanScc } from './scc';
import { minimizeBatches } from './optimize';
import type { PlanRequest, PlanResult, Batch, TogetherReason } from './model';

export class PlanInputError extends Error {}

export const MAX_PROJECTS = 18;

interface Indexed {
  ids: string[];
  depIdx: number[][]; // depIdx[i] = 项目 i 依赖的项目下标（已去重）
}

function validateAndIndex(req: PlanRequest): Indexed {
  if (!req || typeof req !== 'object') throw new PlanInputError('请求必须是对象');
  const cap = req.capacity;
  if (!Number.isInteger(cap) || cap < 1) throw new PlanInputError('capacity 必须是 >= 1 的整数');
  const projects = req.projects;
  if (!Array.isArray(projects) || projects.length < 1) throw new PlanInputError('projects 不能为空');
  if (projects.length > MAX_PROJECTS) {
    throw new PlanInputError(`项目数 ${projects.length} 超过上限 ${MAX_PROJECTS}`);
  }
  const ids: string[] = [];
  const indexOf = new Map<string, number>();
  for (const p of projects) {
    if (!p || typeof p.id !== 'string' || p.id.length === 0) {
      throw new PlanInputError('每个项目都需要非空字符串 id');
    }
    if (indexOf.has(p.id)) throw new PlanInputError(`项目 id 重复: ${p.id}`);
    indexOf.set(p.id, ids.length);
    ids.push(p.id);
  }
  const depIdx: number[][] = [];
  for (const p of projects) {
    const deps = p.deps === undefined ? [] : p.deps;
    if (!Array.isArray(deps)) throw new PlanInputError(`项目 ${p.id} 的 deps 必须是数组`);
    const seen = new Set<number>();
    const list: number[] = [];
    for (const d of deps) {
      const j = indexOf.get(d);
      if (j === undefined) throw new PlanInputError(`项目 ${p.id} 依赖了未知项目: ${d}`);
      if (!seen.has(j)) {
        seen.add(j);
        list.push(j);
      }
    }
    depIdx.push(list);
  }
  return { ids, depIdx };
}

// 在给定节点集合（一个强连通分量）内找一条具体的依赖环。
// 返回值首尾相同，如 [a, b, c, a]，表示 a 依赖 b、b 依赖 c、c 依赖 a。
function findCycle(inComp: boolean[], adj: number[][]): number[] {
  const n = adj.length;
  const state = new Array<number>(n).fill(0); // 0 未访问, 1 在栈上, 2 已完成
  const stack: number[] = [];
  let found: number[] = [];
  const visit = (u: number): boolean => {
    state[u] = 1;
    stack.push(u);
    for (const v of adj[u]) {
      if (!inComp[v]) continue;
      if (state[v] === 1) {
        found = stack.slice(stack.indexOf(v));
        found.push(v);
        return true;
      }
      if (state[v] === 0 && visit(v)) return true;
    }
    stack.pop();
    state[u] = 2;
    return false;
  };
  for (let u = 0; u < n; u++) {
    if (inComp[u] && state[u] === 0 && visit(u)) break;
  }
  return found;
}

export function planRelease(req: PlanRequest): PlanResult {
  const indexed = validateAndIndex(req);
  const ids = indexed.ids;
  const depIdx = indexed.depIdx;
  const capacity = req.capacity;
  const n = ids.length;

  const scc = tarjanScc(n, depIdx);
  const compOf = scc.compOf;
  const comps = scc.comps;
  const m = comps.length;

  // 不可行判定：依赖环比每批容量还大
  for (let c = 0; c < m; c++) {
    if (comps[c].length > capacity) {
      const inComp = new Array<boolean>(n).fill(false);
      for (const u of comps[c]) inComp[u] = true;
      const cyc = findCycle(inComp, depIdx).map((u) => ids[u]);
      const members = comps[c].map((u) => ids[u]);
      return {
        ok: false,
        witness: {
          kind: 'cycle-exceeds-capacity',
          component: members,
          cycle: cyc,
          size: comps[c].length,
          capacity,
          detail:
            `强连通分量 {${members.join(', ')}} 含 ${comps[c].length} 个项目，` +
            `存在依赖环 ${cyc.join(' -> ')}，环上项目必须同批发布，` +
            `但分量大小 ${comps[c].length} 超过每批容量 ${capacity}`,
        },
      };
    }
  }

  // 凝聚 DAG 的前驱位掩码
  const predMask = new Array<number>(m).fill(0);
  for (let i = 0; i < n; i++) {
    for (const j of depIdx[i]) {
      const ci = compOf[i];
      const cj = compOf[j];
      if (ci !== cj) predMask[ci] |= 1 << cj;
    }
  }
  const sizes = comps.map((c) => c.length);
  const compBatches = minimizeBatches(sizes, predMask, capacity);

  // 组装输出：批次 + 同批原因
  const batches: Batch[] = compBatches.map((compList, k) => {
    const projectIds: string[] = [];
    for (const c of compList) {
      for (const u of comps[c]) projectIds.push(ids[u]);
    }
    const reasons: TogetherReason[] = [];
    const cycleProjects = new Set<string>();
    for (const c of compList) {
      if (comps[c].length > 1) {
        const inComp = new Array<boolean>(n).fill(false);
        for (const u of comps[c]) inComp[u] = true;
        const cyc = findCycle(inComp, depIdx).map((u) => ids[u]);
        const members = comps[c].map((u) => ids[u]);
        for (const id of members) cycleProjects.add(id);
        reasons.push({
          projects: members,
          kind: 'cycle',
          detail: `循环依赖 ${cyc.join(' -> ')}：环上项目必须同批发布`,
        });
      }
    }
    const rest = projectIds.filter((id) => !cycleProjects.has(id));
    if (rest.length >= 2) {
      reasons.push({
        projects: rest,
        kind: 'capacity',
        detail: `无循环依赖；在每批容量 ${capacity} 的约束下，为达到最少批次数而同批发布`,
      });
    }
    return { index: k, projects: projectIds, reasons };
  });

  return { ok: true, batchCount: batches.length, batches };
}
