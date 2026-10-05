// 通用有限属性求值器。
//
// - 属性实例以 (节点ID, 属性名) 为缓存键 —— 不使用变量名，
//   因此同名遮蔽、声明移动都不会造成缓存键冲突。
// - 计算属性时记录精确的依赖边（被读属性 -> 读者），并记录读取瞬间
//   被读属性的版本号；依赖边可附带 via 标签（例如未绑定引用对作用域
//   的负依赖：via = "lookup:x"）。
// - 结构编辑后：根因属性被强制重算（force），其余按依赖边传播脏标记；
//   读取时按需验证（verify）：逐条比较依赖的当前版本与记录版本，
//   全部一致则复用旧值，从而实现“只重算受影响属性”的精确增量。
// - 求值栈检测跨属性循环，报错时给出具体依赖路径。

export type NodeId = string;
export type AttrName = string;
export type Key = string; // `${nodeId}.${attrName}`

export interface DepEdge {
  key: Key;
  via?: string;
  version: number; // 读取时被读属性的版本号
}

interface Entry {
  value: unknown;
  valid: boolean; // 至少计算过一次
  dirty: boolean; // 可能过期，读取前需要 verify
  forced: boolean; // 结构性根因：跳过 verify，直接重算
  version: number; // 值每次真正变化时递增
  deps: DepEdge[]; // 本次计算实际读取的属性
  dependents: Map<Key, string | undefined>; // 反向边：谁读了我
}

export class CycleError extends Error {
  readonly path: Key[];
  constructor(path: Key[]) {
    super(`circular attribute dependency: ${path.join(' -> ')}`);
    this.name = 'CycleError';
    this.path = path;
  }
}

export function keyOf(id: NodeId, attr: AttrName): Key {
  return `${id}.${attr}`;
}
export function splitKey(key: Key): [NodeId, AttrName] {
  const dot = key.indexOf('.');
  return [key.slice(0, dot), key.slice(dot + 1)];
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((x, i) => deepEqual(x, (b as unknown[])[i]));
  }
  const ka = Object.keys(a as Record<string, unknown>);
  const kb = Object.keys(b as Record<string, unknown>);
  if (ka.length !== kb.length) return false;
  return ka.every((k) =>
    deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}

export class AttrEvaluator {
  private readonly entries = new Map<Key, Entry>();
  private readonly rules = new Map<AttrName, (id: NodeId) => unknown>();
  private readonly active: Key[] = []; // 求值栈：循环检测 + 路径报告
  private readonly collectors: DepEdge[][] = []; // 每层计算的依赖收集器
  private readonly equal: (a: unknown, b: unknown) => boolean;

  readonly stats = { recomputed: 0, verified: 0, cacheHits: 0 };
  /** 本轮实际重算（执行了规则）的属性键，供测试断言增量性。 */
  readonly lastRecomputed: Key[] = [];

  constructor(equal?: (a: unknown, b: unknown) => boolean) {
    this.equal = equal ?? deepEqual;
  }

  registerRule(attr: AttrName, rule: (id: NodeId) => unknown): void {
    this.rules.set(attr, rule);
  }

  get(id: NodeId, attr: AttrName, via?: string): unknown {
    const key = keyOf(id, attr);
    let e = this.entries.get(key);
    if (e && e.valid && !e.dirty) {
      this.stats.cacheHits++;
    } else {
      this.refresh(key);
      e = this.entries.get(key);
    }
    // 在拿到最新值之后记录依赖（含被读属性的当前版本号）
    this.recordDep(key, via);
    return (e as Entry).value;
  }

  /** 已缓存则返回缓存值，不触发计算（供测试/检查用）。 */
  peek(id: NodeId, attr: AttrName): unknown {
    const e = this.entries.get(keyOf(id, attr));
    return e && e.valid ? e.value : undefined;
  }

  has(id: NodeId, attr: AttrName): boolean {
    const e = this.entries.get(keyOf(id, attr));
    return e !== undefined && e.valid;
  }

  dependenciesOf(id: NodeId, attr: AttrName): DepEdge[] {
    return this.entries.get(keyOf(id, attr))?.deps ?? [];
  }
  dependentsOf(id: NodeId, attr: AttrName): Key[] {
    return [...(this.entries.get(keyOf(id, attr))?.dependents.keys() ?? [])];
  }

  /**
   * 结构编辑后的失效入口。
   * force=true 的属性是“根因”（其规则读取了被改动的 AST 结构），
   * 必须跳过 verify 直接重算；其余属性只标脏，读取时再验证。
   */
  invalidate(keys: Iterable<Key>, opts: { force?: boolean } = {}): void {
    const queue: Key[] = [];
    for (const k of keys) {
      const e = this.entries.get(k);
      if (!e || !e.valid) continue;
      if (opts.force) e.forced = true;
      if (!e.dirty) {
        e.dirty = true;
        queue.push(k);
      }
    }
    while (queue.length > 0) {
      const k = queue.pop() as Key;
      const e = this.entries.get(k);
      if (!e) continue;
      for (const dep of e.dependents.keys()) {
        const d = this.entries.get(dep);
        if (d && d.valid && !d.dirty) {
          d.dirty = true;
          queue.push(dep);
        }
      }
    }
  }

  /** 删除子树的全部缓存属性；先让子树外依赖它们的属性失效。 */
  deleteAttributes(ids: ReadonlySet<NodeId>): void {
    const doomed: Key[] = [];
    for (const k of this.entries.keys()) {
      if (ids.has(splitKey(k)[0])) doomed.push(k);
    }
    const external = new Set<Key>();
    for (const k of doomed) {
      const e = this.entries.get(k) as Entry;
      for (const dep of e.dependents.keys()) {
        if (!ids.has(splitKey(dep)[0])) external.add(dep);
      }
    }
    this.invalidate(external);
    for (const k of doomed) {
      const e = this.entries.get(k) as Entry;
      for (const dep of e.deps) {
        const d = this.entries.get(dep.key);
        if (d) d.dependents.delete(k);
      }
      this.entries.delete(k);
    }
  }

  resetStats(): void {
    this.stats.recomputed = 0;
    this.stats.verified = 0;
    this.stats.cacheHits = 0;
    this.lastRecomputed.length = 0;
  }

  private recordDep(key: Key, via?: string): void {
    const top = this.collectors[this.collectors.length - 1];
    if (!top) return;
    const e = this.entries.get(key);
    top.push({ key, via, version: e ? e.version : -1 });
  }

  private refresh(key: Key): { value: unknown; changed: boolean } {
    const idx = this.active.indexOf(key);
    if (idx >= 0) {
      throw new CycleError([...this.active.slice(idx), key]);
    }
    const e = this.entries.get(key);
    if (e && e.valid && !e.dirty) return { value: e.value, changed: false };
    this.active.push(key);
    try {
      if (e && e.valid && e.dirty && !e.forced) {
        // verify：刷新全部依赖并比较版本；都没变则复用旧值，不执行规则。
        let depChanged = false;
        for (const dep of e.deps) {
          let d = this.entries.get(dep.key);
          if (!d || !d.valid) {
            depChanged = true;
            break;
          }
          if (d.dirty) this.refresh(dep.key);
          d = this.entries.get(dep.key);
          if (!d || d.version !== dep.version) depChanged = true;
        }
        if (!depChanged) {
          e.dirty = false;
          this.stats.verified++;
          return { value: e.value, changed: false };
        }
      }
      if (e) e.forced = false;
      return this.run(key);
    } finally {
      this.active.pop();
    }
  }

  private run(key: Key): { value: unknown; changed: boolean } {
    const [id, attr] = splitKey(key);
    const rule = this.rules.get(attr);
    if (!rule) throw new Error(`no rule registered for attribute '${attr}'`);
    const old = this.entries.get(key) ?? null;
    this.collectors.push([]);
    let value: unknown;
    let changed = true;
    let ok = false;
    try {
      value = rule(id);
      ok = true;
      this.stats.recomputed++;
      this.lastRecomputed.push(key);
    } finally {
      const collected = this.collectors.pop() as DepEdge[];
      if (ok) changed = this.commit(key, old, collected, value);
      // 规则抛错（如循环依赖）时保留旧缓存，异常继续上抛。
    }
    return { value, changed };
  }

  private commit(key: Key, old: Entry | null, deps: DepEdge[], value: unknown): boolean {
    if (old) {
      for (const dep of old.deps) {
        const d = this.entries.get(dep.key);
        if (d) d.dependents.delete(key);
      }
    }
    let changed = true;
    if (old && old.valid && this.equal(old.value, value)) {
      value = old.value; // 值未变：复用旧对象，版本不变，切断下游传播
      changed = false;
    }
    const dependents = old ? old.dependents : new Map<Key, string | undefined>();
    const version = changed ? (old ? old.version + 1 : 1) : (old as Entry).version;
    this.entries.set(key, { value, valid: true, dirty: false, forced: false, version, deps, dependents });
    for (const dep of deps) {
      const d = this.entries.get(dep.key);
      if (d) d.dependents.set(key, dep.via);
    }
    return changed;
  }
}
