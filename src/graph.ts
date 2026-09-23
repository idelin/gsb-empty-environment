import { deepFreeze } from './freeze.ts';

export type Read = <T>(ref: SelectorRef<T>) => T;

export interface SelectorRef<T = unknown> {
  readonly def: SelectorDef<T, never>;
  readonly params: unknown;
}

export interface SelectorDef<T, P = undefined> {
  readonly id: string;
  readonly equal: (a: T, b: T) => boolean;
  readonly equalVersion: number;
  readonly compute: (read: Read, params: P) => T;
  with(params: P): SelectorRef<T>;
}

export interface DefineSelectorOptions<T, P> {
  id: string;
  compute: (read: Read, params: P) => T;
  equal?: (a: T, b: T) => boolean;
  equalVersion?: number;
}

export function defineSelector<T, P = undefined>(options: DefineSelectorOptions<T, P>): SelectorDef<T, P> {
  const def: SelectorDef<T, P> = {
    id: options.id,
    equal: options.equal ?? Object.is,
    equalVersion: options.equalVersion ?? 0,
    compute: options.compute,
    with(params: P): SelectorRef<T> {
      return { def: def as unknown as SelectorDef<T, never>, params };
    },
  };
  return def;
}

export function stableKey(value: unknown): string {
  if (value === undefined) return 'undefined';
  if (value === null || typeof value === 'number' || typeof value === 'boolean' || typeof value === 'string') {
    return JSON.stringify(value) as string;
  }
  if (typeof value === 'function' || typeof value === 'symbol' || typeof value === 'bigint') {
    return JSON.stringify(String(value)) as string;
  }
  if (Array.isArray(value)) return `[${value.map(stableKey).join(',')}]`;
  const entries = Object.keys(value as Record<string, unknown>)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${stableKey((value as Record<string, unknown>)[key])}`);
  return `{${entries.join(',')}}`;
}

interface Node {
  readonly key: string;
  readonly def: SelectorDef<unknown, unknown>;
  readonly params: unknown;
  value: unknown;
  version: number;
  cleanFor: number;
  computed: boolean;
  deps: Map<string, Node>;
  depVersions: Map<string, number>;
  dependents: Set<Node>;
}

export interface GraphHooks {
  freeze: boolean;
  onError: (error: unknown, context: string) => void;
  stats: { recomputes: number; equalityErrors: number };
}

export class SelectorGraph {
  stateVersion = 0;
  private readonly hooks: GraphHooks;
  private readonly instances = new Map<string, Node>();

  constructor(hooks: GraphHooks) {
    this.hooks = hooks;
  }

  advance(): void {
    this.stateVersion++;
  }

  instanceKey<T>(ref: SelectorRef<T>): string {
    return `${ref.def.id}#eq${ref.def.equalVersion}#${stableKey(ref.params)}`;
  }

  instanceCount(): number {
    return this.instances.size;
  }

  dependentsOf<T>(ref: SelectorRef<T>): number {
    const node = this.instances.get(this.instanceKey(ref));
    return node ? node.dependents.size : 0;
  }

  cacheIdentity<T>(ref: SelectorRef<T>): string {
    const key = this.instanceKey(ref);
    const node = this.instances.get(key);
    if (!node) return `${key}|unresolved`;
    const deps = [...node.deps.values()]
      .map((dep) => `${dep.key}@${dep.version}`)
      .sort()
      .join(',');
    return `${node.key}|deps=[${deps}]`;
  }

  read<T>(ref: SelectorRef<T>): T {
    const node = this.getInstance(ref);
    this.ensureFresh(node);
    return node.value as T;
  }

  evaluateReference<T>(ref: SelectorRef<T>): T {
    const read: Read = (inner) => this.evaluateReference(inner);
    return ref.def.compute(read, ref.params as never) as T;
  }

  private getInstance<T>(ref: SelectorRef<T>): Node {
    const key = this.instanceKey(ref);
    let node = this.instances.get(key);
    if (!node) {
      node = {
        key,
        def: ref.def as unknown as SelectorDef<unknown, unknown>,
        params: ref.params,
        value: undefined,
        version: 0,
        cleanFor: -1,
        computed: false,
        deps: new Map(),
        depVersions: new Map(),
        dependents: new Set(),
      };
      this.instances.set(key, node);
    }
    return node;
  }

  private ensureFresh(node: Node): void {
    if (node.cleanFor === this.stateVersion) return;
    let depsChanged = !node.computed || node.deps.size === 0;
    if (node.computed && node.deps.size > 0) {
      for (const dep of node.deps.values()) this.ensureFresh(dep);
      for (const [key, dep] of node.deps) {
        if (dep.version !== node.depVersions.get(key)) {
          depsChanged = true;
          break;
        }
      }
    }
    if (!depsChanged) {
      node.cleanFor = this.stateVersion;
      return;
    }
    this.recompute(node);
  }

  private recompute(node: Node): void {
    this.hooks.stats.recomputes++;
    const nextDeps = new Map<string, Node>();
    const read: Read = (ref) => {
      const dep = this.getInstance(ref);
      this.ensureFresh(dep);
      nextDeps.set(dep.key, dep);
      dep.dependents.add(node);
      return dep.value;
    };
    let value: unknown;
    try {
      value = node.def.compute(read, node.params);
    } catch (error) {
      this.hooks.onError(error, `compute:${node.def.id}`);
      node.cleanFor = this.stateVersion;
      return;
    }
    for (const [key, dep] of node.deps) {
      if (!nextDeps.has(key)) dep.dependents.delete(node);
    }
    node.deps = nextDeps;
    node.depVersions = new Map();
    for (const [key, dep] of nextDeps) node.depVersions.set(key, dep.version);
    const changed = !node.computed || !this.safeEqual(node.def, node.value, value);
    node.value = this.hooks.freeze ? deepFreeze(value) : value;
    node.computed = true;
    node.cleanFor = this.stateVersion;
    if (changed) node.version++;
  }

  private safeEqual(def: SelectorDef<unknown, unknown>, a: unknown, b: unknown): boolean {
    try {
      return def.equal(a, b);
    } catch (error) {
      this.hooks.stats.equalityErrors++;
      this.hooks.onError(error, `equal:${def.id}`);
      return false;
    }
  }
}
