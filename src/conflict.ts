import type {
  ConflictSpec,
  DepSpec,
  RootRequirement,
  SolveInput,
} from './types.ts';
import { formatRange } from './range.ts';
import { Solver } from './solver.ts';

/**
 * 一条可独立复核的约束事实。
 * 冲突子集由若干这样的约束组成，可以据此重建一个更小的求解问题。
 */
export type ConstraintItem =
  | { kind: 'root'; req: RootRequirement }
  | { kind: 'requires'; pkg: string; version: number; dep: DepSpec }
  | { kind: 'conflicts'; pkg: string; version: number; spec: ConflictSpec };

/** 无解时返回的报告：一个关于删除极小的不可满足约束子集。 */
export interface ConflictReport {
  /** 结构化约束子集。 */
  constraints: ConstraintItem[];
  /** 人类可读的约束描述，与 constraints 一一对应。 */
  summary: string[];
  /**
   * 复核该冲突子集：
   * 1. 仅由该子集重建的问题仍然无解；
   * 2. 子集是极小的——去掉其中任意一条约束后问题即可解。
   * 两项都满足时返回 true。
   */
  verify(): boolean;
}

/** 把一条约束格式化为人类可读描述。 */
export function describeConstraint(item: ConstraintItem): string {
  switch (item.kind) {
    case 'root': {
      const feats = item.req.features;
      const suffix = feats !== undefined && feats.length > 0 ? `（启用特性: ${feats.join(', ')}）` : '';
      return `要求安装 ${item.req.pkg}@${formatRange(item.req.range)}${suffix}`;
    }
    case 'requires': {
      const feat = item.dep.feature !== undefined ? ` [特性 ${item.dep.feature}]` : '';
      const opt = item.dep.optional === true ? '（软依赖）' : '';
      return `${item.pkg}@${item.version} 依赖 ${item.dep.pkg}@${formatRange(item.dep.range)}${feat}${opt}`;
    }
    case 'conflicts':
      return `${item.pkg}@${item.version} 与 ${item.spec.pkg}@${formatRange(item.spec.range)} 冲突`;
  }
}

/** 收集从根需求可达的全部约束事实（根需求 + 相关包各版本的依赖/冲突边）。 */
function buildAllConstraints(input: SolveInput): ConstraintItem[] {
  const byName = new Map(input.repository.map((d) => [d.name, d]));
  const reachable = new Set<string>();
  const queue = input.required.map((r) => r.pkg);
  while (queue.length > 0) {
    const name = queue.pop()!;
    if (reachable.has(name)) continue;
    reachable.add(name);
    const def = byName.get(name);
    if (def === undefined) continue;
    for (const v of def.versions) {
      for (const d of v.requires ?? []) queue.push(d.pkg);
      for (const c of v.conflicts ?? []) queue.push(c.pkg);
    }
  }

  const items: ConstraintItem[] = input.required.map((req) => ({ kind: 'root', req }));
  for (const def of input.repository) {
    if (!reachable.has(def.name)) continue;
    for (const v of def.versions) {
      for (const dep of v.requires ?? []) {
        items.push({ kind: 'requires', pkg: def.name, version: v.version, dep });
      }
      for (const spec of v.conflicts ?? []) {
        items.push({ kind: 'conflicts', pkg: def.name, version: v.version, spec });
      }
    }
  }
  return items;
}

/** 用约束子集重建一个更小的求解问题（只保留子集中的根需求与依赖/冲突边）。 */
function rebuild(input: SolveInput, items: ConstraintItem[]): SolveInput {
  const required: RootRequirement[] = [];
  const reqEdges = new Map<string, DepSpec[]>();
  const confEdges = new Map<string, ConflictSpec[]>();
  const mentioned = new Set<string>();

  const push = <T>(map: Map<string, T[]>, key: string, value: T): void => {
    const list = map.get(key);
    if (list === undefined) map.set(key, [value]);
    else list.push(value);
  };

  for (const item of items) {
    if (item.kind === 'root') {
      required.push(item.req);
      mentioned.add(item.req.pkg);
    } else if (item.kind === 'requires') {
      mentioned.add(item.pkg);
      mentioned.add(item.dep.pkg);
      push(reqEdges, `${item.pkg}@${item.version}`, item.dep);
    } else {
      mentioned.add(item.pkg);
      mentioned.add(item.spec.pkg);
      push(confEdges, `${item.pkg}@${item.version}`, item.spec);
    }
  }

  const repository = input.repository
    .filter((d) => mentioned.has(d.name))
    .map((d) => ({
      name: d.name,
      versions: d.versions.map((v) => {
        const key = `${d.name}@${v.version}`;
        return {
          version: v.version,
          requires: reqEdges.get(key) ?? [],
          conflicts: confEdges.get(key) ?? [],
        };
      }),
    }));

  return { repository, required };
}

/**
 * 计算一个关于删除极小的不可满足约束子集（MUS）。
 * 反复尝试删除每条约束：若删除后问题仍无解，则永久移除，
 * 直到不动点。调用前需保证整体问题无解。
 */
export function minimalUnsatisfiableSubset(input: SolveInput): ConstraintItem[] {
  let items = buildAllConstraints(input);
  let changed = true;
  while (changed) {
    changed = false;
    for (const item of [...items]) {
      const trial = items.filter((x) => x !== item);
      if (!new Solver(rebuild(input, trial)).feasible()) {
        items = trial;
        changed = true;
      }
    }
  }
  return items;
}

/** 为无解输入构建可复核的冲突报告。 */
export function buildConflictReport(input: SolveInput): ConflictReport {
  const constraints = minimalUnsatisfiableSubset(input);
  return {
    constraints,
    summary: constraints.map(describeConstraint),
    verify(): boolean {
      if (new Solver(rebuild(input, constraints)).feasible()) return false;
      return constraints.every((item) =>
        new Solver(rebuild(input, constraints.filter((x) => x !== item))).feasible(),
      );
    },
  };
}
