import { compareVersions, satisfies } from './semver.ts';
import type { PluginManifest, RootRequest } from './manifest.ts';

export interface ConflictExplanation {
  /** 一句话结论 */
  summary: string;
  /** 最小约束集合：删掉其中任意一条，冲突即消失 */
  constraints: string[];
  /** 涉及的插件名 */
  plugins: string[];
}

export interface RejectedPlugin {
  name: string;
  request?: string;
  explanation: ConflictExplanation;
}

export interface LoadPlan {
  /** 拓扑序（依赖在前），只包含可独立工作的插件 */
  order: PluginManifest[];
  /** 被拒绝的根请求 / 被排除的插件及其最小冲突解释 */
  rejected: RejectedPlugin[];
  /** 可选依赖启用 / 跳过等说明 */
  notes: string[];
}

type Constraint =
  | { kind: 'root'; name: string; range: string; label: string }
  | { kind: 'include'; name: string; range: string; label: string }
  | { kind: 'exclude'; name: string; label: string }
  | { kind: 'requires'; from: string; fromVersion: string; dep: string; range: string; label: string }
  | { kind: 'conflict'; name: string; version: string; other: string; range: string; label: string };

function constraintNames(constraint: Constraint): string[] {
  switch (constraint.kind) {
    case 'root':
    case 'include':
    case 'exclude':
      return [constraint.name];
    case 'requires':
      return [constraint.from, constraint.dep];
    case 'conflict':
      return [constraint.name, constraint.other];
  }
}

interface Problem {
  versions: Map<string, string[]>;
  constraints: Constraint[];
}

/** 回溯求解：为每个插件选择一个版本或“缺席”，满足全部约束；不可满足返回 null */
function solve(problem: Problem): Map<string, string | null> | null {
  const { versions, constraints } = problem;
  const involved = new Set<string>();
  for (const constraint of constraints) {
    for (const name of constraintNames(constraint)) involved.add(name);
  }
  const pinned = new Map<string, string[]>();
  const excluded = new Set<string>();
  for (const constraint of constraints) {
    if (constraint.kind === 'root' || constraint.kind === 'include') {
      const ranges = pinned.get(constraint.name) ?? [];
      ranges.push(constraint.range);
      pinned.set(constraint.name, ranges);
    } else if (constraint.kind === 'exclude') {
      excluded.add(constraint.name);
    }
  }
  const domains = new Map<string, (string | null)[]>();
  for (const name of involved) {
    const all = versions.get(name) ?? [];
    if (excluded.has(name)) {
      domains.set(name, [null]);
      continue;
    }
    const ranges = pinned.get(name);
    if (ranges) domains.set(name, all.filter((v) => ranges.every((r) => satisfies(v, r))));
    else domains.set(name, [null, ...all]);
  }
  const names = [...involved].sort((a, b) => {
    const diff = domains.get(a)!.length - domains.get(b)!.length;
    return diff !== 0 ? diff : a < b ? -1 : 1;
  });
  const assignment = new Map<string, string | null>();
  const hasFeasibleValue = (name: string, pred: (v: string | null) => boolean): boolean =>
    (domains.get(name) ?? [null]).some(pred);

  function consistent(): boolean {
    for (const constraint of constraints) {
      switch (constraint.kind) {
        case 'root':
        case 'include': {
          const value = assignment.get(constraint.name);
          if (value !== undefined && (value === null || !satisfies(value, constraint.range))) return false;
          break;
        }
        case 'exclude': {
          const value = assignment.get(constraint.name);
          if (value !== undefined && value !== null) return false;
          break;
        }
        case 'requires': {
          if (assignment.get(constraint.from) === constraint.fromVersion) {
            const depValue = assignment.get(constraint.dep);
            if (depValue !== undefined) {
              if (depValue === null || !satisfies(depValue, constraint.range)) return false;
            } else if (!hasFeasibleValue(constraint.dep, (v) => v !== null && satisfies(v, constraint.range))) {
              return false;
            }
          }
          break;
        }
        case 'conflict': {
          if (assignment.get(constraint.name) === constraint.version) {
            const otherValue = assignment.get(constraint.other);
            if (otherValue !== undefined) {
              if (otherValue !== null && satisfies(otherValue, constraint.range)) return false;
            } else if (!hasFeasibleValue(constraint.other, (v) => v === null || !satisfies(v, constraint.range))) {
              return false;
            }
          }
          break;
        }
      }
    }
    return true;
  }

  function visit(index: number): boolean {
    if (index === names.length) return true;
    const name = names[index];
    for (const value of domains.get(name)!) {
      assignment.set(name, value);
      if (consistent() && visit(index + 1)) return true;
    }
    assignment.delete(name);
    return false;
  }

  return visit(0) ? assignment : null;
}

/** 删除式极小化：反复移除“去掉后仍不可满足”的约束，得到最小不可满足子集 */
function minimizeUnsat(problem: Problem): Constraint[] {
  let current = [...problem.constraints];
  for (const constraint of [...current]) {
    if (current.length === 1) break;
    const trial = current.filter((c) => c !== constraint);
    if (solve({ ...problem, constraints: trial }) === null) current = trial;
  }
  return current;
}

function explain(constraints: Constraint[], summary: string): ConflictExplanation {
  return {
    summary,
    constraints: constraints.map((c) => c.label),
    plugins: [...new Set(constraints.flatMap(constraintNames))].sort(),
  };
}

interface Graph {
  names: string[];
  /** dependent -> 它依赖的插件 */
  edges: Map<string, Set<string>>;
}

function buildGraph(
  assignment: Map<string, string | null>,
  manifests: Map<string, PluginManifest>,
): Graph {
  const names = [...assignment.entries()].filter(([, v]) => v !== null).map(([n]) => n);
  const edges = new Map<string, Set<string>>();
  for (const name of names) {
    const manifest = manifests.get(`${name}@${assignment.get(name)}`)!;
    const deps = edges.get(name) ?? new Set<string>();
    for (const dep of Object.keys(manifest.requires ?? {})) {
      if (assignment.get(dep)) deps.add(dep);
    }
    for (const dep of Object.keys(manifest.optional ?? {})) {
      if (assignment.get(dep)) deps.add(dep);
    }
    edges.set(name, deps);
  }
  return { names, edges };
}

function findCycle(graph: Graph): string[] | null {
  const state = new Map<string, number>();
  const stack: string[] = [];
  const visit = (name: string): string[] | null => {
    state.set(name, 1);
    stack.push(name);
    for (const next of graph.edges.get(name) ?? []) {
      const s = state.get(next) ?? 0;
      if (s === 1) return stack.slice(stack.indexOf(next));
      if (s === 0) {
        const found = visit(next);
        if (found) return found;
      }
    }
    stack.pop();
    state.set(name, 2);
    return null;
  };
  for (const name of graph.names) {
    if (!state.get(name)) {
      const found = visit(name);
      if (found) return found;
    }
  }
  return null;
}

/** 依赖在前的拓扑序（图必须无环） */
function topoSort(graph: Graph, priority: string[]): string[] {
  const result: string[] = [];
  const done = new Set<string>();
  const visit = (name: string): void => {
    if (done.has(name)) return;
    done.add(name);
    for (const dep of graph.edges.get(name) ?? []) visit(dep);
    result.push(name);
  };
  for (const name of priority) if (graph.edges.has(name) || graph.names.includes(name)) visit(name);
  for (const name of graph.names) visit(name);
  return result;
}

interface ActiveRoot {
  name: string;
  range: string;
  index: number;
}

export function createPlan(registry: PluginManifest[], roots: RootRequest[]): LoadPlan {
  const versions = new Map<string, string[]>();
  const manifests = new Map<string, PluginManifest>();
  for (const manifest of registry) {
    const key = `${manifest.name}@${manifest.version}`;
    if (manifests.has(key)) throw new Error(`注册表中存在重复的插件版本: ${key}`);
    manifests.set(key, manifest);
    const list = versions.get(manifest.name) ?? [];
    list.push(manifest.version);
    versions.set(manifest.name, list);
  }
  for (const list of versions.values()) list.sort((a, b) => compareVersions(b, a));

  const notes: string[] = [];
  const rejected: RejectedPlugin[] = [];
  const activeRoots: ActiveRoot[] = roots.map((r, index) => ({ ...r, range: r.range ?? '*', index }));
  const excluded = new Map<string, string>();
  const optionalIncludes = new Map<string, string>();

  const staticConstraints: Constraint[] = [];
  for (const manifest of registry) {
    for (const [dep, range] of Object.entries(manifest.requires ?? {})) {
      staticConstraints.push({
        kind: 'requires',
        from: manifest.name,
        fromVersion: manifest.version,
        dep,
        range,
        label: `${manifest.name}@${manifest.version} 依赖 ${dep}@${range}`,
      });
    }
    for (const [other, range] of Object.entries(manifest.conflicts ?? {})) {
      staticConstraints.push({
        kind: 'conflict',
        name: manifest.name,
        version: manifest.version,
        other,
        range,
        label: `${manifest.name}@${manifest.version} 与 ${other}@${range} 冲突`,
      });
    }
  }

  const buildConstraints = (): Constraint[] => {
    const constraints: Constraint[] = [];
    for (const root of activeRoots) {
      constraints.push({ kind: 'root', name: root.name, range: root.range, label: `请求 ${root.name}@${root.range}` });
    }
    for (const [name, reason] of excluded) {
      constraints.push({ kind: 'exclude', name, label: `${name} 被排除（${reason}）` });
    }
    for (const [name, range] of optionalIncludes) {
      constraints.push({ kind: 'include', name, range, label: `可选依赖 ${name}@${range} 被启用` });
    }
    return [...constraints, ...staticConstraints];
  };

  // 1) 硬约束求解；不可满足时提取最小冲突解释，拒绝最后请求的相关根，保留可独立工作的插件
  let assignment: Map<string, string | null> | null = null;
  for (;;) {
    let constraints = buildConstraints();
    assignment = solve({ versions, constraints });
    while (assignment === null) {
      const mus = minimizeUnsat({ versions, constraints });
      const rootConstraints = mus.filter((c): c is Extract<Constraint, { kind: 'root' }> => c.kind === 'root');
      if (rootConstraints.length === 0) {
        throw new Error('规划失败：约束不可满足且不涉及任何根请求');
      }
      const victim = rootConstraints
        .map((c) => activeRoots.find((r) => r.name === c.name && r.range === c.range)!)
        .sort((a, b) => b.index - a.index)[0];
      activeRoots.splice(activeRoots.indexOf(victim), 1);
      rejected.push({
        name: victim.name,
        request: victim.range,
        explanation: explain(mus, `无法加载 ${victim.name}：存在不可同时满足的约束（最小集合 ${mus.length} 条）`),
      });
      constraints = buildConstraints();
      assignment = solve({ versions, constraints });
    }
    // 2) 循环依赖检测：排除环中插件（优先非根），重新求解
    const cycle = findCycle(buildGraph(assignment, manifests));
    if (cycle === null) break;
    const rootNames = new Set(activeRoots.map((r) => r.name));
    const nonRoots = cycle.filter((n) => !rootNames.has(n)).sort();
    const victim =
      nonRoots.length > 0
        ? nonRoots[0]
        : [...cycle].sort(
            (a, b) => activeRoots.find((r) => r.name === b)!.index - activeRoots.find((r) => r.name === a)!.index,
          )[0];
    const reason = `循环依赖 ${[...cycle, cycle[0]].join(' -> ')}`;
    const rootEntry = activeRoots.find((r) => r.name === victim);
    if (rootEntry) activeRoots.splice(activeRoots.indexOf(rootEntry), 1);
    excluded.set(victim, reason);
    rejected.push({ name: victim, explanation: { summary: reason, constraints: [reason], plugins: [...cycle].sort() } });
  }

  // 3) 可选依赖：逐个尝试启用，保持整体可满足且无环，否则跳过
  let changed = true;
  while (changed) {
    changed = false;
    for (const [name, version] of [...assignment]) {
      if (version === null) continue;
      const manifest = manifests.get(`${name}@${version}`)!;
      for (const [dep, range] of Object.entries(manifest.optional ?? {})) {
        if (assignment.get(dep) || optionalIncludes.has(dep) || excluded.has(dep)) continue;
        optionalIncludes.set(dep, range);
        const trial = solve({ versions, constraints: buildConstraints() });
        if (trial !== null && findCycle(buildGraph(trial, manifests)) === null) {
          assignment = trial;
          changed = true;
          notes.push(`已为 ${name}@${version} 启用可选依赖 ${dep}@${range}`);
        } else {
          optionalIncludes.delete(dep);
          notes.push(`已跳过 ${name}@${version} 的可选依赖 ${dep}@${range}（无兼容版本或引发冲突）`);
        }
      }
    }
  }

  // 4) 拓扑排序输出加载顺序
  const graph = buildGraph(assignment, manifests);
  const priority = roots.map((r) => r.name);
  const order = topoSort(graph, priority).map((name) => manifests.get(`${name}@${assignment!.get(name)}`)!);
  return { order, rejected, notes };
}
