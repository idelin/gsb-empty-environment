/**
 * 时间旅行状态库（无响应式框架依赖）
 *
 * 核心规则：
 * - 状态操作（op）是纯函数 reducer，随参数一起记入提交，可任意回放。
 * - 副作用（effect）只在实时提交时执行一次，结果记入提交；回放/查看历史绝不重放。
 * - 事务（transact）原子提交：提交前读者只能看到旧状态，监听器只通知一次。
 * - 派生值（derived）按顶层状态切片依赖缓存，依赖引用不变则不重算；
 *   回到历史时按历史状态重新计算。
 * - 在历史提交上产生新操作时自动分叉新分支，原实时分支历史不被覆盖。
 */

export interface OpRecord {
  op: string;
  args: unknown[];
}

export interface EffectRecord {
  kind: string;
  args: unknown[];
  result: unknown;
}

export interface Commit {
  id: string;
  branch: string;
  parent: string | null;
  ops: OpRecord[];
  effects: EffectRecord[];
  time: number;
}

type Reducer<S> = (state: S, ...args: any[]) => S;
type EffectHandler = (...args: any[]) => unknown;
type Listener<S> = (state: S) => void;

interface DerivedDef<S> {
  deps: string[];
  compute: (state: S) => unknown;
}

export class Store<S extends Record<string, unknown>> {
  private readonly baseState: S;
  private readonly reducers = new Map<string, Reducer<S>>();
  private readonly effectHandlers = new Map<string, EffectHandler>();
  private readonly derivedDefs = new Map<string, DerivedDef<S>>();
  private readonly derivedCache = new Map<string, { deps: unknown[]; value: unknown }>();
  private readonly recomputeCounts = new Map<string, number>();

  private readonly commits = new Map<string, Commit>();
  private readonly branches = new Map<string, string | null>();
  private currentBranch = 'main';
  private headId: string | null = null;
  private state: S;
  private commitCounter = 0;
  private forkCounter = 0;

  private txDepth = 0;
  private txState: S | null = null;
  private txOps: OpRecord[] = [];
  private txEffects: EffectRecord[] = [];

  private readonly listeners = new Set<Listener<S>>();

  constructor(initialState: S) {
    this.baseState = initialState;
    this.state = initialState;
    this.branches.set('main', null);
  }

  defineOp(name: string, reducer: Reducer<S>): void {
    this.reducers.set(name, reducer);
  }

  defineEffect(kind: string, handler: EffectHandler): void {
    this.effectHandlers.set(kind, handler);
  }

  defineDerived<T>(name: string, deps: string[], compute: (state: S) => T): void {
    this.derivedDefs.set(name, { deps, compute: compute as (state: S) => unknown });
  }

  /** 当前是否处于实时模式（HEAD 位于当前分支尖端）。 */
  get mode(): 'live' | 'replay' {
    return this.headId === (this.branches.get(this.currentBranch) ?? null) ? 'live' : 'replay';
  }

  get branch(): string {
    return this.currentBranch;
  }

  get head(): string | null {
    return this.headId;
  }

  /** 读者只能看到已提交状态；事务进行中返回事务前的状态。 */
  getState(): S {
    return this.state;
  }

  subscribe(fn: Listener<S>): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /** 单操作便捷提交。 */
  dispatch(op: string, ...args: unknown[]): void {
    this.transact(() => this.run(op, ...args));
  }

  /**
   * 原子事务：fn 内的所有操作先作用于草稿状态，全部成功后才一次性提交。
   * 提交前 getState()/derived() 仍返回旧状态，监听器只在提交后通知一次。
   */
  transact(fn: () => void): void {
    const outer = this.txDepth === 0;
    if (outer) {
      this.ensureWritableBranch();
      this.txState = this.state;
      this.txOps = [];
      this.txEffects = [];
    }
    this.txDepth++;
    try {
      fn();
    } catch (err) {
      if (outer) {
        this.txDepth = 0;
        this.txState = null;
        this.txOps = [];
        this.txEffects = [];
      }
      throw err;
    }
    this.txDepth--;
    if (!outer) return;

    // 副作用只在实时提交时执行一次，结果随提交记录，供回放时查阅。
    for (const fx of this.txEffects) {
      const handler = this.effectHandlers.get(fx.kind);
      if (!handler) throw new Error(`unknown effect: ${fx.kind}`);
      fx.result = handler(...fx.args);
    }

    const commit: Commit = {
      id: `c${++this.commitCounter}`,
      branch: this.currentBranch,
      parent: this.headId,
      ops: this.txOps,
      effects: this.txEffects,
      time: Date.now(),
    };
    this.commits.set(commit.id, commit);
    this.branches.set(this.currentBranch, commit.id);
    this.headId = commit.id;
    this.state = this.txState!;
    this.txState = null;
    this.txOps = [];
    this.txEffects = [];
    this.notify();
  }

  /** 在事务内应用一个纯操作（可回放）。 */
  run(op: string, ...args: unknown[]): void {
    if (this.txDepth === 0) {
      this.dispatch(op, ...args);
      return;
    }
    const reducer = this.reducers.get(op);
    if (!reducer) throw new Error(`unknown op: ${op}`);
    this.txState = reducer(this.txState!, ...args);
    this.txOps.push({ op, args });
  }

  /** 在事务内登记一个副作用；仅在实时提交时执行一次。 */
  emit(kind: string, ...args: unknown[]): void {
    if (this.txDepth === 0) throw new Error('emit() must be called inside a transaction');
    this.txEffects.push({ kind, args, result: undefined });
  }

  /** 派生值：依赖切片引用全部不变时返回缓存，否则按当前（可能是历史）状态重算。 */
  derived<T>(name: string): T {
    const def = this.derivedDefs.get(name);
    if (!def) throw new Error(`unknown derived: ${name}`);
    const depVals = def.deps.map((d) => this.state[d]);
    const cached = this.derivedCache.get(name);
    if (
      cached &&
      cached.deps.length === depVals.length &&
      cached.deps.every((v, i) => Object.is(v, depVals[i]))
    ) {
      return cached.value as T;
    }
    const value = def.compute(this.state);
    this.derivedCache.set(name, { deps: depVals, value });
    this.recomputeCounts.set(name, (this.recomputeCounts.get(name) ?? 0) + 1);
    return value as T;
  }

  /** 派生值实际重算次数（用于验证按依赖重算）。 */
  recomputeCount(name: string): number {
    return this.recomputeCounts.get(name) ?? 0;
  }

  /** 回到历史提交：只重放纯操作，副作用不重复执行。 */
  checkout(commitId: string): void {
    const target = this.commits.get(commitId);
    if (!target) throw new Error(`unknown commit: ${commitId}`);
    const chain: Commit[] = [];
    let cur: Commit | undefined = target;
    while (cur) {
      chain.unshift(cur);
      cur = cur.parent ? this.commits.get(cur.parent) : undefined;
    }
    let s = this.baseState;
    for (const c of chain) {
      for (const rec of c.ops) {
        s = this.reducers.get(rec.op)!(s, ...rec.args);
      }
    }
    this.state = s;
    this.headId = commitId;
    this.currentBranch = target.branch;
    this.notify();
  }

  /** 切回当前分支尖端，恢复实时模式。 */
  backToLive(): void {
    const tip = this.branches.get(this.currentBranch) ?? null;
    if (tip === null) {
      this.headId = null;
      this.state = this.baseState;
      this.notify();
      return;
    }
    if (tip !== this.headId) this.checkout(tip);
  }

  /** 切换到指定分支并落在其尖端（实时模式）。 */
  switchBranch(name: string): void {
    if (!this.branches.has(name)) throw new Error(`unknown branch: ${name}`);
    this.currentBranch = name;
    this.backToLive();
  }

  /** 查看提交记录（含副作用结果），不会触发任何副作用。 */
  getCommit(id: string): Commit | undefined {
    return this.commits.get(id);
  }

  /** 分支完整历史（从根到尖端）。 */
  history(branch = this.currentBranch): Commit[] {
    const out: Commit[] = [];
    let id = this.branches.get(branch) ?? null;
    while (id) {
      const c = this.commits.get(id)!;
      out.unshift(c);
      id = c.parent;
    }
    return out;
  }

  listBranches(): Array<{ name: string; tip: string | null }> {
    return [...this.branches.entries()].map(([name, tip]) => ({ name, tip }));
  }

  /** HEAD 不在当前分支尖端时，新操作自动分叉，原分支历史保持不变。 */
  private ensureWritableBranch(): void {
    const tip = this.branches.get(this.currentBranch) ?? null;
    if (this.headId !== tip) {
      const name = `fork-${++this.forkCounter}`;
      this.branches.set(name, this.headId);
      this.currentBranch = name;
    }
  }

  private notify(): void {
    for (const fn of this.listeners) fn(this.state);
  }
}
