import type { InstallPlan, Range, RootRequirement, SolveInput } from './types.ts';
import { inRange, intersectRange } from './range.ts';

export interface NormDep {
  target: string;
  range: Range;
  feature?: string;
  optional?: boolean;
}

export interface NormConflict {
  target: string;
  range: Range;
}

interface NormVersion {
  version: number;
  requires: NormDep[];
  conflicts: NormConflict[];
}

export interface SolveOptions {
  /** 搜索节点上限，防止病态输入导致指数爆炸，默认 200000。 */
  maxNodes?: number;
}

/** 搜索节点数超过上限时抛出。 */
export class SearchLimitError extends Error {
  readonly nodes: number;
  constructor(nodes: number) {
    super(`搜索节点数超过上限（${nodes}），无法在限制内完成求解`);
    this.name = 'SearchLimitError';
    this.nodes = nodes;
  }
}

export interface Best {
  plan: InstallPlan;
  cost: number;
}

interface Propagation {
  /** 当前必须安装的包。 */
  active: Set<string>;
  /** 每个活跃包当前可行的版本集合（降序）。 */
  domains: Map<string, number[]>;
}

const DEFAULT_MAX_NODES = 200_000;

/**
 * 依赖求解器。
 *
 * 采用深度优先回溯 + 约束传播（版本区间收缩、冲突排除、特性门控依赖激活），
 * 并以分支限界优化目标：
 *   1. 最小化升级数（与 installed 不一致的包数量，含新装）；
 *   2. 升级数并列时，按包名字典序取版本向量字典序最大者（即优先更高版本）。
 */
export class Solver {
  private readonly names: string[];
  private readonly versions = new Map<string, Map<number, NormVersion>>();
  private readonly sortedVersions = new Map<string, number[]>();
  private readonly roots: RootRequirement[];
  private readonly features = new Map<string, Set<string>>();
  private readonly installed: Record<string, number>;
  private readonly maxNodes: number;
  private nodes = 0;
  private best: Best | null = null;
  private stopAtFirst = false;
  private readonly assigned = new Map<string, number>();

  constructor(input: SolveInput, options: SolveOptions = {}) {
    validateInput(input);
    this.maxNodes = options.maxNodes ?? DEFAULT_MAX_NODES;
    this.installed = input.installed ?? {};
    this.roots = input.required.map((r) => ({ ...r }));
    for (const req of this.roots) {
      if (req.features === undefined) continue;
      let set = this.features.get(req.pkg);
      if (set === undefined) {
        set = new Set();
        this.features.set(req.pkg, set);
      }
      for (const f of req.features) set.add(f);
    }
    const names = new Set<string>();
    for (const def of input.repository) {
      names.add(def.name);
      const vmap = new Map<number, NormVersion>();
      const list: number[] = [];
      for (const v of def.versions) {
        vmap.set(v.version, {
          version: v.version,
          requires: (v.requires ?? []).map((d) => ({
            target: d.pkg,
            range: d.range ?? {},
            feature: d.feature,
            optional: d.optional,
          })),
          conflicts: (v.conflicts ?? []).map((c) => ({
            target: c.pkg,
            range: c.range ?? {},
          })),
        });
        list.push(v.version);
      }
      list.sort((a, b) => b - a);
      this.versions.set(def.name, vmap);
      this.sortedVersions.set(def.name, list);
    }
    this.names = [...names].sort();
  }

  /** 只判断是否存在可行解（找到第一个即停）。 */
  feasible(): boolean {
    this.reset(true);
    this.dfs();
    return this.best !== null;
  }

  /** 求最优解：升级数最少，并列时版本向量字典序最大。 */
  optimize(): Best | null {
    this.reset(false);
    this.dfs();
    return this.best;
  }

  private reset(stopAtFirst: boolean): void {
    this.nodes = 0;
    this.best = null;
    this.stopAtFirst = stopAtFirst;
    this.assigned.clear();
  }

  /** 返回 true 表示可以提前终止整个搜索。 */
  private dfs(): boolean {
    if (++this.nodes > this.maxNodes) throw new SearchLimitError(this.maxNodes);
    const prop = this.propagate();
    if (prop === null) return false;

    if (this.best !== null) {
      // 分支限界：当前部分解的升级数下界已超过最优值时剪枝。
      let lowerBound = 0;
      for (const [name, ver] of this.assigned) {
        if (this.installed[name] !== ver) lowerBound++;
      }
      for (const name of prop.active) {
        if (this.assigned.has(name)) continue;
        const inst = this.installed[name];
        const dom = prop.domains.get(name)!;
        if (inst === undefined || !dom.includes(inst)) lowerBound++;
      }
      if (lowerBound > this.best.cost) return false;
    }

    // MRV：选可行域最小的未赋值活跃包。
    let pick: string | null = null;
    let pickDomain: number[] = [];
    for (const name of prop.active) {
      if (this.assigned.has(name)) continue;
      const dom = prop.domains.get(name)!;
      if (dom.length === 0) return false;
      if (pick === null || dom.length < pickDomain.length) {
        pick = name;
        pickDomain = dom;
      }
    }

    if (pick === null) {
      this.recordSolution();
      return this.stopAtFirst;
    }

    // 版本从高到低尝试，配合并列时取高版本的规则。
    for (const ver of pickDomain) {
      this.assigned.set(pick, ver);
      const stop = this.dfs();
      this.assigned.delete(pick);
      if (stop) return true;
    }
    return false;
  }

  /**
   * 约束传播：根据当前赋值重算活跃包集合与各自可行域。
   * 返回 null 表示当前分支已不可行。
   */
  private propagate(): Propagation | null {
    const active = new Set<string>();
    const rangeAcc = new Map<string, Range>();
    const exclusions = new Map<string, Range[]>();

    const addRange = (pkg: string, r: Range): boolean => {
      const merged = intersectRange(rangeAcc.get(pkg) ?? {}, r);
      if (merged === null) return false;
      rangeAcc.set(pkg, merged);
      return true;
    };

    // 第一阶段：确定哪些包必须安装（根需求 + 已赋值包的强制依赖）。
    for (const req of this.roots) active.add(req.pkg);
    for (const [pkg, ver] of this.assigned) {
      const info = this.versions.get(pkg)?.get(ver);
      if (info === undefined) return null;
      const feats = this.features.get(pkg);
      for (const dep of info.requires) {
        if (dep.optional) continue;
        if (dep.feature !== undefined && !(feats?.has(dep.feature) ?? false)) continue;
        active.add(dep.target);
      }
    }

    // 第二阶段：收集版本区间与冲突排除（软依赖仅在目标已活跃时生效）。
    for (const req of this.roots) {
      if (req.range !== undefined && !addRange(req.pkg, req.range)) return null;
    }
    for (const [pkg, ver] of this.assigned) {
      const info = this.versions.get(pkg)!.get(ver)!;
      const feats = this.features.get(pkg);
      for (const dep of info.requires) {
        if (dep.feature !== undefined && !(feats?.has(dep.feature) ?? false)) continue;
        if (!active.has(dep.target)) continue;
        if (!addRange(dep.target, dep.range)) return null;
      }
      for (const c of info.conflicts) {
        const list = exclusions.get(c.target);
        if (list === undefined) exclusions.set(c.target, [c.range]);
        else list.push(c.range);
      }
    }

    // 计算每个活跃包的可行域。
    const domains = new Map<string, number[]>();
    for (const name of active) {
      const all = this.sortedVersions.get(name);
      if (all === undefined) return null; // 仓库中不存在该包
      const r = rangeAcc.get(name) ?? {};
      const excl = exclusions.get(name) ?? [];
      domains.set(
        name,
        all.filter((v) => inRange(v, r) && !excl.some((e) => inRange(v, e))),
      );
    }

    // 已赋值包必须仍然可行，且不与其它已赋值包直接冲突。
    for (const [pkg, ver] of this.assigned) {
      const dom = domains.get(pkg);
      if (dom === undefined || !dom.includes(ver)) return null;
      const info = this.versions.get(pkg)!.get(ver)!;
      for (const c of info.conflicts) {
        const other = this.assigned.get(c.target);
        if (other !== undefined && inRange(other, c.range)) return null;
      }
    }

    return { active, domains };
  }

  private recordSolution(): void {
    const plan: InstallPlan = {};
    let cost = 0;
    for (const [name, ver] of this.assigned) {
      plan[name] = ver;
      if (this.installed[name] !== ver) cost++;
    }
    if (
      this.best === null ||
      cost < this.best.cost ||
      (cost === this.best.cost && lexGreater(plan, this.best.plan, this.names))
    ) {
      this.best = { plan, cost };
    }
  }
}

/** 按包名字典序比较两个方案的版本向量，a 更大（版本更高/安装更多）时返回 true。 */
function lexGreater(a: InstallPlan, b: InstallPlan, names: string[]): boolean {
  for (const n of names) {
    const av = a[n] ?? -1;
    const bv = b[n] ?? -1;
    if (av !== bv) return av > bv;
  }
  return false;
}

function validateInput(input: SolveInput): void {
  const seen = new Set<string>();
  for (const def of input.repository) {
    if (def.name.length === 0) throw new Error('包名不能为空');
    if (seen.has(def.name)) throw new Error(`仓库中存在重复的包: ${def.name}`);
    seen.add(def.name);
    const vseen = new Set<number>();
    for (const v of def.versions) {
      checkVersion(v.version, def.name);
      if (vseen.has(v.version)) {
        throw new Error(`包 ${def.name} 存在重复版本 ${v.version}`);
      }
      vseen.add(v.version);
      for (const d of v.requires ?? []) checkRange(d.range, `${def.name}@${v.version} 的依赖`);
      for (const c of v.conflicts ?? []) checkRange(c.range, `${def.name}@${v.version} 的冲突`);
    }
  }
  for (const req of input.required) {
    if (req.pkg.length === 0) throw new Error('根需求的包名不能为空');
    checkRange(req.range, `根需求 ${req.pkg}`);
  }
  for (const [name, ver] of Object.entries(input.installed ?? {})) {
    checkVersion(ver, name);
  }
}

function checkVersion(v: number, ctx: string): void {
  if (!Number.isInteger(v) || v < 0) {
    throw new Error(`包 ${ctx} 的版本必须是非负整数，得到 ${v}`);
  }
}

function checkRange(r: Range | undefined, ctx: string): void {
  if (r === undefined) return;
  if (r.min !== undefined && (!Number.isInteger(r.min) || r.min < 0)) {
    throw new Error(`${ctx} 的区间下界必须是非负整数，得到 ${r.min}`);
  }
  if (r.max !== undefined && (!Number.isInteger(r.max) || r.max < 0)) {
    throw new Error(`${ctx} 的区间上界必须是非负整数，得到 ${r.max}`);
  }
  if (r.min !== undefined && r.max !== undefined && r.min > r.max) {
    throw new Error(`${ctx} 的区间下界大于上界（${r.min} > ${r.max}）`);
  }
}
