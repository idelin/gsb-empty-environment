import type { NodeDef } from './types.js';

// 审批计划：节点定义 + 前后依赖关系（DAG）。
export class ApprovalPlan {
  private nodes: Map<string, NodeDef>;

  constructor(defs: NodeDef[]) {
    this.nodes = new Map();
    for (const def of defs) {
      this.nodes.set(def.id, def);
    }
  }

  static validate(defs: NodeDef[]): string[] {
    const errors: string[] = [];
    const seen: Set<string> = new Set();
    for (const def of defs) {
      if (seen.has(def.id)) {
        errors.push(`节点 ${def.id} 重复定义`);
      }
      seen.add(def.id);
      if (def.assignees.length === 0) {
        errors.push(`节点 ${def.id} 缺少审批人`);
      }
      if (def.deps.indexOf(def.id) >= 0) {
        errors.push(`节点 ${def.id} 不能依赖自身`);
      }
      for (const dep of def.deps) {
        if (defs.every(d => d.id !== dep)) {
          errors.push(`节点 ${def.id} 依赖了不存在的节点 ${dep}`);
        }
      }
    }
    if (errors.length === 0) {
      const plan = new ApprovalPlan(defs);
      const cycle = plan.findCycle();
      if (cycle.length > 0) {
        errors.push(`依赖存在环路，涉及节点：${cycle.join('、')}`);
      }
    }
    return errors;
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  get(id: string): NodeDef {
    const def = this.nodes.get(id);
    if (!def) {
      throw new Error(`节点 ${id} 不存在`);
    }
    return def;
  }

  ids(): string[] {
    return Array.from(this.nodes.keys());
  }

  depsOf(id: string): string[] {
    return this.get(id).deps.slice();
  }

  dependentsOf(id: string): string[] {
    const result: string[] = [];
    for (const def of this.nodes.values()) {
      if (def.deps.indexOf(id) >= 0) {
        result.push(def.id);
      }
    }
    return result;
  }

  addNode(def: NodeDef): void {
    this.nodes.set(def.id, def);
  }

  removeNode(id: string): void {
    this.nodes.delete(id);
  }

  addDependency(from: string, to: string): void {
    this.get(to).deps.push(from);
  }

  clone(): ApprovalPlan {
    const defs: NodeDef[] = [];
    for (const def of this.nodes.values()) {
      defs.push({ id: def.id, label: def.label, mode: def.mode, assignees: def.assignees.slice(), deps: def.deps.slice() });
    }
    return new ApprovalPlan(defs);
  }

  // 沿“from 必须先于其后继执行”的方向寻找 from 到 to 的路径，找不到返回空数组。
  findPath(from: string, to: string): string[] {
    if (from === to) {
      return [from];
    }
    const queue: string[] = [from];
    const prev: Map<string, string> = new Map();
    const seen: Set<string> = new Set();
    seen.add(from);
    while (queue.length > 0) {
      const cur = queue.shift();
      if (cur === undefined) {
        break;
      }
      for (const nxt of this.dependentsOf(cur)) {
        if (seen.has(nxt)) {
          continue;
        }
        seen.add(nxt);
        prev.set(nxt, cur);
        if (nxt === to) {
          const path: string[] = [to];
          let walk = to;
          while (walk !== from) {
            const parent = prev.get(walk);
            if (parent === undefined) {
              break;
            }
            path.unshift(parent);
            walk = parent;
          }
          return path;
        }
        queue.push(nxt);
      }
    }
    return [];
  }

  // Kahn 拓扑排序，返回无法入队的节点（即环上的节点）；无环时返回空数组。
  findCycle(): string[] {
    const indeg: Map<string, number> = new Map();
    for (const id of this.nodes.keys()) {
      indeg.set(id, 0);
    }
    for (const def of this.nodes.values()) {
      for (const dep of def.deps) {
        if (this.nodes.has(dep)) {
          indeg.set(def.id, (indeg.get(def.id) || 0) + 1);
        }
      }
    }
    const queue: string[] = [];
    for (const id of this.nodes.keys()) {
      if ((indeg.get(id) || 0) === 0) {
        queue.push(id);
      }
    }
    let count = 0;
    while (queue.length > 0) {
      const cur = queue.shift();
      if (cur === undefined) {
        break;
      }
      count++;
      for (const nxt of this.dependentsOf(cur)) {
        const d = (indeg.get(nxt) || 0) - 1;
        indeg.set(nxt, d);
        if (d === 0) {
          queue.push(nxt);
        }
      }
    }
    if (count === this.nodes.size) {
      return [];
    }
    const rest: string[] = [];
    for (const id of this.nodes.keys()) {
      if ((indeg.get(id) || 0) > 0) {
        rest.push(id);
      }
    }
    return rest;
  }
}
