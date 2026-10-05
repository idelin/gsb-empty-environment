import { compareVersions, parseVersion, satisfies } from "./semver.ts";
import type { PluginModule, Registry } from "./types.ts";

export const REQUEST = "<request>";

export interface PlanEdge {
  from: string;
  to: string;
  range: string;
  kind: "require" | "optional" | "conflict";
}

export type ExplanationKind = "conflict" | "missing-dependency" | "unsatisfiable-version";

export interface ConflictExplanation {
  kind: ExplanationKind;
  summary: string;
  edges: PlanEdge[];
}

export interface PlanRequest {
  name: string;
  range?: string;
}

export interface RejectedRequest {
  request: PlanRequest;
  explanation: ConflictExplanation;
}

export interface OptionalSkip {
  parent: string;
  name: string;
  range: string;
  reason: string;
}

export interface Plan {
  selected: Map<string, PluginModule>;
  requestEdges: PlanEdge[];
  edges: PlanEdge[];
  order: string[];
  rejected: RejectedRequest[];
  optionalSkipped: OptionalSkip[];
}

interface PendingEdge {
  from: string;
  to: string;
  range: string;
  kind: "require" | "optional";
}

interface ConflictPair {
  a: string;
  b: string;
  range: string;
}

type AttemptResult =
  | { ok: true; selected: Map<string, PluginModule>; optionalSkipped: OptionalSkip[] }
  | { ok: false; explanation: ConflictExplanation };

export function emptyPlan(): Plan {
  return {
    selected: new Map(),
    requestEdges: [],
    edges: [],
    order: [],
    rejected: [],
    optionalSkipped: [],
  };
}

function edgeKey(edge: PlanEdge): string {
  return `${edge.from}|${edge.to}|${edge.range}|${edge.kind}`;
}

function dedupeEdges(edges: PlanEdge[]): PlanEdge[] {
  const seen = new Set<string>();
  const result: PlanEdge[] = [];
  for (const edge of edges) {
    const key = edgeKey(edge);
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(edge);
  }
  return result;
}

function reachableFromRequest(edges: PlanEdge[]): Set<string> {
  const bySource = new Map<string, PlanEdge[]>();
  for (const edge of edges) {
    if (edge.kind === "conflict") continue;
    const list = bySource.get(edge.from) ?? [];
    list.push(edge);
    bySource.set(edge.from, list);
  }
  const seen = new Set<string>([REQUEST]);
  const queue = [REQUEST];
  while (queue.length > 0) {
    const node = queue.shift()!;
    for (const edge of bySource.get(node) ?? []) {
      if (seen.has(edge.to)) continue;
      seen.add(edge.to);
      queue.push(edge.to);
    }
  }
  return seen;
}

function minimizeEdges(edges: PlanEdge[], mustReach: string[], protectedKeys: Set<string>): PlanEdge[] {
  let result = dedupeEdges(edges);
  let changed = true;
  while (changed) {
    changed = false;
    for (const edge of [...result]) {
      if (edge.kind === "conflict") continue;
      if (protectedKeys.has(edgeKey(edge))) continue;
      const rest = result.filter((candidate) => candidate !== edge);
      const reachable = reachableFromRequest(rest);
      if (mustReach.every((target) => reachable.has(target))) {
        result = rest;
        changed = true;
      }
    }
  }
  return result;
}

export function topoOrder(selected: Map<string, PluginModule>): string[] {
  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const name of selected.keys()) {
    indegree.set(name, 0);
    dependents.set(name, []);
  }
  for (const [name, module] of selected) {
    const deps = { ...module.manifest.requires, ...module.manifest.optional };
    for (const dep of Object.keys(deps)) {
      if (!selected.has(dep)) continue;
      indegree.set(name, (indegree.get(name) ?? 0) + 1);
      dependents.get(dep)!.push(name);
    }
  }
  const queue = [...selected.keys()].filter((name) => indegree.get(name) === 0);
  const order: string[] = [];
  while (queue.length > 0) {
    const name = queue.shift()!;
    order.push(name);
    for (const dependent of dependents.get(name) ?? []) {
      indegree.set(dependent, indegree.get(dependent)! - 1);
      if (indegree.get(dependent) === 0) queue.push(dependent);
    }
  }
  if (order.length !== selected.size) throw new Error("插件依赖存在循环，无法规划初始化顺序");
  return order;
}

export class Planner {
  private readonly registry: Registry;

  constructor(registry: Registry) {
    this.registry = registry;
  }

  plan(requests: PlanRequest[]): Plan {
    const result = emptyPlan();
    for (const request of requests) this.extend(result, request);
    return result;
  }

  extend(plan: Plan, request: PlanRequest): boolean {
    const seed: PendingEdge = {
      from: REQUEST,
      to: request.name,
      range: request.range ?? "*",
      kind: "require",
    };
    const result = this.attempt([seed], plan.selected, plan.requestEdges);
    if (!result.ok) {
      plan.rejected.push({ request, explanation: result.explanation });
      return false;
    }
    plan.selected = result.selected;
    this.mergeSkips(plan.optionalSkipped, result.optionalSkipped);
    plan.requestEdges.push({ from: REQUEST, to: request.name, range: seed.range, kind: "require" });
    plan.edges = buildEdges(plan.selected, plan.requestEdges);
    plan.order = topoOrder(plan.selected);
    return true;
  }

  private mergeSkips(target: OptionalSkip[], incoming: OptionalSkip[]): void {
    const seen = new Set(target.map((skip) => `${skip.parent}|${skip.name}`));
    for (const skip of incoming) {
      const key = `${skip.parent}|${skip.name}`;
      if (seen.has(key)) continue;
      seen.add(key);
      target.push(skip);
    }
  }

  private attempt(
    seeds: PendingEdge[],
    base: Map<string, PluginModule>,
    requestEdges: PlanEdge[],
  ): AttemptResult {
    const selected = new Map(base);
    const fixed = new Set(base.keys());
    const constraints = new Map<string, string[]>();
    const pending = [...seeds];

    const satisfiesAll = (version: string, name: string): boolean =>
      (constraints.get(name) ?? []).every((range) => satisfies(version, range));

    while (pending.length > 0) {
      const edge = pending.shift()!;
      const list = constraints.get(edge.to) ?? [];
      list.push(edge.range);
      constraints.set(edge.to, list);

      const current = selected.get(edge.to);
      if (current && satisfiesAll(current.manifest.version, edge.to)) continue;
      if (current && fixed.has(edge.to)) {
        return { ok: false, explanation: this.explainUnsatisfiable(selected, requestEdges, seeds, edge, current) };
      }
      const candidates = (this.registry.get(edge.to) ?? [])
        .filter((module) => satisfiesAll(module.manifest.version, edge.to))
        .sort((x, y) => compareVersions(parseVersion(y.manifest.version), parseVersion(x.manifest.version)));
      if (candidates.length === 0) {
        return { ok: false, explanation: this.explainUnresolvable(selected, requestEdges, seeds, edge) };
      }
      selected.set(edge.to, candidates[0]);
      for (const [dep, range] of Object.entries(candidates[0].manifest.requires ?? {})) {
        pending.push({ from: edge.to, to: dep, range, kind: "require" });
      }
    }

    this.pruneUnreachable(selected, fixed, seeds);

    const requiredConflict = this.findConflict(selected);
    if (requiredConflict) {
      return { ok: false, explanation: this.explainConflict(selected, requestEdges, seeds, requiredConflict) };
    }

    const optionalSkipped: OptionalSkip[] = [];
    const skippedKeys = new Set<string>();
    const recordSkip = (skip: OptionalSkip): void => {
      const key = `${skip.parent}|${skip.name}`;
      if (skippedKeys.has(key)) return;
      skippedKeys.add(key);
      optionalSkipped.push(skip);
    };

    const optionalQueue: PendingEdge[] = [];
    const enqueueOptionals = (module: PluginModule): void => {
      for (const [dep, range] of Object.entries(module.manifest.optional ?? {})) {
        optionalQueue.push({ from: module.manifest.name, to: dep, range, kind: "optional" });
      }
    };
    for (const module of selected.values()) enqueueOptionals(module);

    while (optionalQueue.length > 0) {
      const edge = optionalQueue.shift()!;
      const existing = selected.get(edge.to);
      if (existing) {
        if (!satisfies(existing.manifest.version, edge.range)) {
          recordSkip({
            parent: edge.from,
            name: edge.to,
            range: edge.range,
            reason: `已选版本 ${existing.manifest.version} 不满足可选约束 ${edge.range}`,
          });
        }
        continue;
      }
      const sub = this.attempt([edge], selected, requestEdges);
      if (!sub.ok) {
        recordSkip({ parent: edge.from, name: edge.to, range: edge.range, reason: sub.explanation.summary });
        continue;
      }
      for (const [name, module] of sub.selected) {
        if (!selected.has(name)) {
          selected.set(name, module);
          enqueueOptionals(module);
        }
      }
      this.mergeSkips(optionalSkipped, sub.optionalSkipped);
      for (const skip of sub.optionalSkipped) skippedKeys.add(`${skip.parent}|${skip.name}`);
    }

    return { ok: true, selected, optionalSkipped };
  }

  private pruneUnreachable(selected: Map<string, PluginModule>, fixed: Set<string>, seeds: PendingEdge[]): void {
    const reachable = new Set<string>();
    const queue: string[] = [];
    for (const seed of seeds) queue.push(seed.to);
    for (const name of fixed) queue.push(name);
    while (queue.length > 0) {
      const name = queue.pop()!;
      if (reachable.has(name)) continue;
      const module = selected.get(name);
      if (!module) continue;
      reachable.add(name);
      for (const dep of Object.keys(module.manifest.requires ?? {})) queue.push(dep);
    }
    for (const name of [...selected.keys()]) {
      if (!reachable.has(name)) selected.delete(name);
    }
  }

  private findConflict(selected: Map<string, PluginModule>): ConflictPair | null {
    for (const [name, module] of selected) {
      for (const [other, range] of Object.entries(module.manifest.conflicts ?? {})) {
        const target = selected.get(other);
        if (target && satisfies(target.manifest.version, range)) return { a: name, b: other, range };
      }
    }
    return null;
  }

  private contextEdges(
    selected: Map<string, PluginModule>,
    requestEdges: PlanEdge[],
    seeds: PendingEdge[],
  ): PlanEdge[] {
    const seedEdges: PlanEdge[] = seeds.map((seed) => ({
      from: seed.from,
      to: seed.to,
      range: seed.range,
      kind: seed.kind,
    }));
    return buildEdges(selected, [...requestEdges, ...seedEdges]);
  }

  private explainUnresolvable(
    selected: Map<string, PluginModule>,
    requestEdges: PlanEdge[],
    seeds: PendingEdge[],
    edge: PendingEdge,
  ): ConflictExplanation {
    const context = this.contextEdges(selected, requestEdges, seeds);
    const chain = this.chainTo(context, edge.from);
    const terminal: PlanEdge = { from: edge.from, to: edge.to, range: edge.range, kind: edge.kind };
    const known = this.registry.get(edge.to) ?? [];
    const mustReach = edge.from === REQUEST ? [] : [edge.from];
    const edges = minimizeEdges([...chain, terminal], mustReach, new Set([edgeKey(terminal)]));
    if (known.length === 0) {
      return {
        kind: "missing-dependency",
        summary: `缺少前置插件 ${edge.to}@${edge.range}（由 ${edge.from} 要求）`,
        edges,
      };
    }
    return {
      kind: "unsatisfiable-version",
      summary: `插件 ${edge.to} 没有满足 ${edge.range} 的版本（注册表已有: ${known
        .map((module) => module.manifest.version)
        .join(", ")}）`,
      edges,
    };
  }

  private explainUnsatisfiable(
    selected: Map<string, PluginModule>,
    requestEdges: PlanEdge[],
    seeds: PendingEdge[],
    edge: PendingEdge,
    current: PluginModule,
  ): ConflictExplanation {
    const context = this.contextEdges(selected, requestEdges, seeds);
    const chain = this.chainTo(context, edge.to);
    const terminal: PlanEdge = { from: edge.from, to: edge.to, range: edge.range, kind: edge.kind };
    const edges = minimizeEdges([...chain, terminal], [edge.to], new Set([edgeKey(terminal)]));
    return {
      kind: "unsatisfiable-version",
      summary: `插件 ${edge.to} 已固定为 ${current.manifest.version}，无法满足 ${edge.from} 要求的 ${edge.range}`,
      edges,
    };
  }

  private explainConflict(
    selected: Map<string, PluginModule>,
    requestEdges: PlanEdge[],
    seeds: PendingEdge[],
    conflict: ConflictPair,
  ): ConflictExplanation {
    const context = this.contextEdges(selected, requestEdges, seeds);
    const chainA = this.chainTo(context, conflict.a);
    const chainB = this.chainTo(context, conflict.b);
    const conflictEdge: PlanEdge = { from: conflict.a, to: conflict.b, range: conflict.range, kind: "conflict" };
    const edges = minimizeEdges(
      [...chainA, ...chainB, conflictEdge],
      [conflict.a, conflict.b],
      new Set([edgeKey(conflictEdge)]),
    );
    const versionB = selected.get(conflict.b)?.manifest.version ?? "未知";
    return {
      kind: "conflict",
      summary: `插件 ${conflict.a} 声明与 ${conflict.b}@${conflict.range} 冲突（已选 ${conflict.b} 版本 ${versionB}）`,
      edges,
    };
  }

  private chainTo(edges: PlanEdge[], target: string): PlanEdge[] {
    if (target === REQUEST) return [];
    const bySource = new Map<string, PlanEdge[]>();
    for (const edge of edges) {
      if (edge.kind === "conflict") continue;
      const list = bySource.get(edge.from) ?? [];
      list.push(edge);
      bySource.set(edge.from, list);
    }
    const visited = new Set<string>([REQUEST]);
    const previous = new Map<string, PlanEdge>();
    const queue: string[] = [REQUEST];
    while (queue.length > 0) {
      const node = queue.shift()!;
      if (node === target) break;
      for (const edge of bySource.get(node) ?? []) {
        if (visited.has(edge.to)) continue;
        visited.add(edge.to);
        previous.set(edge.to, edge);
        queue.push(edge.to);
      }
    }
    if (!previous.has(target)) return [];
    const chain: PlanEdge[] = [];
    let cursor = target;
    while (cursor !== REQUEST) {
      const edge = previous.get(cursor)!;
      chain.unshift(edge);
      cursor = edge.from;
    }
    return chain;
  }
}

function buildEdges(selected: Map<string, PluginModule>, requestEdges: PlanEdge[]): PlanEdge[] {
  const edges: PlanEdge[] = [...requestEdges];
  for (const [name, module] of selected) {
    for (const [dep, range] of Object.entries(module.manifest.requires ?? {})) {
      if (selected.has(dep)) edges.push({ from: name, to: dep, range, kind: "require" });
    }
    for (const [dep, range] of Object.entries(module.manifest.optional ?? {})) {
      if (selected.has(dep)) edges.push({ from: name, to: dep, range, kind: "optional" });
    }
  }
  return dedupeEdges(edges);
}
