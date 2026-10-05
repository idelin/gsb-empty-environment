// 测试公共工具：独立校验计划与见证

import { tarjanScc } from '../src/scc';
import { planRelease } from '../src/planner';
import type { PlanRequest, PlanResult } from '../src/model';

export function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error('断言失败: ' + msg);
}

// 独立校验：计划是否满足全部约束（与求解器实现无关）
export function validatePlan(req: PlanRequest, result: PlanResult): string | null {
  if (!result.ok) return '期望可行但返回了不可行';
  const cap = req.capacity;
  const comp = componentsOf(req);
  const compOfId = new Map<string, number>();
  {
    // 由 predMask 无法反查分量成员，这里重新算一次 SCC 归属
    const indexOf = new Map<string, number>();
    req.projects.forEach((p, i) => indexOf.set(p.id, i));
    const n = req.projects.length;
    const adj: number[][] = [];
    for (let i = 0; i < n; i++) adj.push([]);
    req.projects.forEach((p, i) => {
      for (const d of p.deps) {
        const j = indexOf.get(d);
        if (j !== undefined) adj[i].push(j);
      }
    });
    const scc = tarjanScc(n, adj);
    req.projects.forEach((p, i) => compOfId.set(p.id, scc.compOf[i]));
  }
  const batchOf = new Map<string, number>();
  for (const b of result.batches) {
    if (b.projects.length === 0) return `批次 ${b.index} 为空`;
    if (b.projects.length > cap) return `批次 ${b.index} 超容量`;
    for (const id of b.projects) {
      if (batchOf.has(id)) return `项目 ${id} 被重复安排`;
      batchOf.set(id, b.index);
    }
  }
  for (const p of req.projects) {
    const pb = batchOf.get(p.id);
    if (pb === undefined) return `项目 ${p.id} 未被安排`;
    for (const d of p.deps) {
      const db = batchOf.get(d);
      if (db === undefined) return `依赖 ${d} 未被安排`;
      if (compOfId.get(d) === compOfId.get(p.id)) {
        if (db !== pb) return `项目 ${p.id} 与 ${d} 同环却不同批`;
      } else if (db >= pb) {
        return `项目 ${p.id} 未排在依赖 ${d} 严格之后`;
      }
    }
  }
  return null;
}

// 校验见证中的环：每条相邻边都是真实依赖，且首尾相同
export function validateCycle(req: PlanRequest, cycle: string[]): boolean {
  if (cycle.length < 2 || cycle[0] !== cycle[cycle.length - 1]) return false;
  const depsOf = new Map<string, string[]>();
  for (const p of req.projects) depsOf.set(p.id, p.deps);
  for (let i = 0; i + 1 < cycle.length; i++) {
    const deps = depsOf.get(cycle[i]);
    if (!deps || deps.indexOf(cycle[i + 1]) < 0) return false;
  }
  return true;
}

// 校验见证中的分量确实是强连通的（互相可达）
export function validateStronglyConnected(req: PlanRequest, component: string[]): boolean {
  const inComp = new Set<string>(component);
  const depsOf = new Map<string, string[]>();
  for (const p of req.projects) depsOf.set(p.id, p.deps);
  for (const start of component) {
    const seen = new Set<string>([start]);
    const queue = [start];
    while (queue.length > 0) {
      const u = queue.pop();
      if (u === undefined) break;
      for (const v of depsOf.get(u) || []) {
        if (inComp.has(v) && !seen.has(v)) {
          seen.add(v);
          queue.push(v);
        }
      }
    }
    if (seen.size !== component.length) return false;
  }
  return true;
}

// 由请求构造分量信息（供对拍用）
export function componentsOf(req: PlanRequest): { sizes: number[]; predMask: number[] } {
  const indexOf = new Map<string, number>();
  req.projects.forEach((p, i) => indexOf.set(p.id, i));
  const n = req.projects.length;
  const adj: number[][] = [];
  for (let i = 0; i < n; i++) adj.push([]);
  req.projects.forEach((p, i) => {
    for (const d of p.deps) {
      const j = indexOf.get(d);
      if (j !== undefined) adj[i].push(j);
    }
  });
  const scc = tarjanScc(n, adj);
  const m = scc.comps.length;
  const predMask = new Array<number>(m).fill(0);
  for (let i = 0; i < n; i++) {
    for (const j of adj[i]) {
      if (scc.compOf[i] !== scc.compOf[j]) predMask[scc.compOf[i]] |= 1 << scc.compOf[j];
    }
  }
  return { sizes: scc.comps.map((c) => c.length), predMask };
}

// 对拍：精确解 vs 暴力解 + 独立校验
export function checkInstance(req: PlanRequest, brute: number | null, ctx: string): void {
  const result: PlanResult = planRelease(req);
  if (brute === null) {
    assert(!result.ok, `${ctx}: 期望不可行`);
    const w = result.witness;
    assert(w.size > req.capacity, `${ctx}: 见证分量大小应超容量`);
    assert(validateCycle(req, w.cycle), `${ctx}: 见证环无效 ${w.cycle.join('->')}`);
    assert(validateStronglyConnected(req, w.component), `${ctx}: 见证分量非强连通`);
    for (const id of w.cycle) {
      assert(w.component.indexOf(id) >= 0, `${ctx}: 环节点 ${id} 不在分量内`);
    }
    return;
  }
  const err = validatePlan(req, result);
  assert(err === null, `${ctx}: 计划无效: ${err}`);
  if (result.ok) {
    assert(result.batchCount === brute, `${ctx}: 批次数 ${result.batchCount} != 暴力最优 ${brute}`);
  }
}
