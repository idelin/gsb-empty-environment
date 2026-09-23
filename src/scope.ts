import { AlreadyCanceledError, BorrowError, DependencyCycleError, ScopeDestroyedError, errorMessage } from "./errors.ts";
import { Journal, serializeError, type SerializedError } from "./journal.ts";
import type { OwnershipLedger } from "./ledger.ts";
import type { ManualScheduler } from "./scheduler.ts";

export interface AcquireOptions {
  /** 本作用域内已注册资源的 key，构成依赖图（被依赖者后清理）。 */
  deps?: readonly string[];
  /** 提供给工厂的取消信号。 */
  signal?: AbortSignal;
}

export interface AcquireHandle<T> {
  readonly key: string;
  readonly promise: Promise<T>;
  /** 幂等取消；仅当工厂尚未完成时生效。重复取消抛 AlreadyCanceledError。 */
  cancel(reason?: unknown): void;
}

/** 借用租约：归还幂等。 */
export interface Lease<T> {
  readonly key: string;
  readonly value: T;
  release(): void;
}

interface PendingRequest {
  key: string;
  deps: readonly string[];
  cancelRequested: boolean;
  cancelReason: unknown;
  userSettled: boolean;
  resolveUser: (value: unknown) => void;
  rejectUser: (err: unknown) => void;
}

interface RuntimeEntry {
  value: unknown;
  destroyer: ((value: never) => unknown) | undefined;
}

let scopeCounter = 0;

export class Scope {
  readonly id: string;
  readonly journal: Journal;

  #scheduler: ManualScheduler;
  #ledger: OwnershipLedger;
  #parent: Scope | null;
  #children: Scope[] = [];
  #requests = new Map<string, PendingRequest>();
  #runtime = new Map<string, RuntimeEntry>();
  #heldLeases = new Set<Lease<unknown>>();
  #pendingCount = 0;
  #destroyPromise: Promise<void> | null = null;
  #destroyErrors: Error[] = [];

  constructor(opts: {
    scheduler: ManualScheduler;
    ledger: OwnershipLedger;
    parent?: Scope | null;
    id?: string;
  }) {
    this.#scheduler = opts.scheduler;
    this.#ledger = opts.ledger;
    this.#parent = opts.parent ?? null;
    this.id = opts.id ?? `scope-${++scopeCounter}`;
    this.journal = new Journal(this.id, this.#parent?.id ?? null);
    if (this.#parent) {
      this.#parent.#children.push(this);
      this.#parent.journal.commit({ type: "childRegistered", childId: this.id });
    }
  }

  get status(): "active" | "destroying" | "destroyed" {
    return this.journal.state.scope.status;
  }

  /** 创建子作用域；父已关闭时拒绝。 */
  createChild(id?: string): Scope {
    if (this.status !== "active") {
      throw new ScopeDestroyedError(`cannot create child of ${this.id}: scope is ${this.status}`);
    }
    return new Scope({ scheduler: this.#scheduler, ledger: this.#ledger, parent: this, id });
  }

  /**
   * 异步获取资源。注册是同步的（进入依赖图），完成是异步的。
   * 作用域关闭后再 acquire 一律同步拒绝。
   */
  acquire<T>(
    key: string,
    factory: (ctx: { signal: AbortSignal }) => Promise<T> | T,
    opts: AcquireOptions & { dispose?: (value: T) => unknown } = {},
  ): AcquireHandle<T> {
    if (this.status !== "active") {
      throw new ScopeDestroyedError(`cannot acquire "${key}": scope ${this.id} is ${this.status}`);
    }
    if (this.#requests.has(key)) {
      throw new Error(`duplicate resource key "${key}" in scope ${this.id}`);
    }
    const deps = opts.deps ?? [];
    for (const dep of deps) {
      if (!this.#requests.has(dep)) {
        throw new Error(`unknown dependency "${dep}" for resource "${key}" in scope ${this.id}`);
      }
    }

    const controller = new AbortController();
    if (opts.signal) {
      if (opts.signal.aborted) controller.abort(opts.signal.reason);
      else opts.signal.addEventListener("abort", () => controller.abort(opts.signal!.reason), { once: true });
    }

    let resolveUser!: (value: T) => void;
    let rejectUser!: (err: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
      resolveUser = res;
      rejectUser = rej;
    });
    // 用户可能不消费 promise；内部已接管所有结算，避免未处理拒绝。
    promise.catch(() => {});

    const request: PendingRequest = {
      key,
      deps,
      cancelRequested: false,
      cancelReason: undefined,
      userSettled: false,
      resolveUser: resolveUser as (value: unknown) => void,
      rejectUser: rejectUser,
    };
    this.#requests.set(key, request);
    this.#pendingCount++;
    this.journal.commit({ type: "register", id: key, key, deps });

    const onSuccess = (value: T) => {
      this.#scheduler.post(() => this.#settleSuccess(request, value, opts.dispose as ((v: unknown) => unknown) | undefined));
    };
    const onFailure = (err: unknown) => {
      this.#scheduler.post(() => this.#settleFailure(request, err));
    };

    let factoryResult: Promise<T>;
    try {
      factoryResult = Promise.resolve(factory({ signal: controller.signal }));
    } catch (err) {
      factoryResult = Promise.reject(err);
    }
    factoryResult.then(onSuccess, onFailure);

    return {
      key,
      promise,
      cancel: (reason?: unknown) => {
        if (request.cancelRequested) throw new AlreadyCanceledError(`acquire "${key}" already canceled`);
        request.cancelRequested = true;
        request.cancelReason = reason;
        controller.abort(reason);
        // 立即向用户结算（经调度器，顺序确定）；迟到结果仍会被立即归还。
        this.#scheduler.post(() => {
          if (request.userSettled) return;
          request.userSettled = true;
          this.journal.commit({ type: "cancel", id: key });
          request.rejectUser(
            new AlreadyCanceledError(`acquire "${key}" canceled: ${errorMessage(reason)}`),
          );
        });
      },
    };
  }

  #settleSuccess(
    request: PendingRequest,
    value: unknown,
    dispose: ((v: unknown) => unknown) | undefined,
  ): void {
    this.#pendingCount--;
    this.#ledger.acquire(value, `${this.id}/${request.key}`);

    if (this.status !== "active" || request.cancelRequested) {
      // 迟到取得 / 取消后取得：立即归还（清理），不计入作用域状态。
      const why = request.cancelRequested
        ? `canceled: ${errorMessage(request.cancelReason)}`
        : `scope ${this.id} is ${this.status}`;
      if (request.cancelRequested) {
        // cancel op 已在取消时提交。
      } else {
        this.journal.commit({ type: "fail", id: request.key, error: serializeError(new ScopeDestroyedError(why)) });
      }
      this.#releaseLate(request.key, value, dispose, request);
      return;
    }

    this.#runtime.set(request.key, { value, destroyer: dispose as ((value: never) => unknown) | undefined });
    this.journal.commit({ type: "ready", id: request.key });
    if (!request.userSettled) {
      request.userSettled = true;
      request.resolveUser(value);
    }
  }

  #settleFailure(request: PendingRequest, err: unknown): void {
    this.#pendingCount--;
    if (request.userSettled) return; // 取消已向用户结算
    request.userSettled = true;
    this.journal.commit(
      request.cancelRequested
        ? { type: "cancel", id: request.key }
        : { type: "fail", id: request.key, error: serializeError(err) },
    );
    request.rejectUser(
      request.cancelRequested
        ? new AlreadyCanceledError(`acquire "${request.key}" canceled: ${errorMessage(request.cancelReason)}`)
        : err instanceof Error
          ? err
          : new Error(String(err)),
    );
  }

  /**
   * 显式添加依赖边 dependentKey -> dependencyKey（清理时 dependent 先释放）。
   * 可用于构造环；环在销毁时按确定规则破环并聚合报告。
   */
  link(dependentKey: string, dependencyKey: string): void {
    if (this.status !== "active") {
      throw new ScopeDestroyedError(`cannot link in scope ${this.id}: scope is ${this.status}`);
    }
    if (!this.#requests.has(dependentKey) || !this.#requests.has(dependencyKey)) {
      throw new Error(`link requires both keys registered in scope ${this.id}`);
    }
    this.journal.commit({ type: "link", id: dependentKey, dep: dependencyKey });
  }

  /** 迟到/取消资源的立即归还，作为独立调度任务执行。 */
  #releaseLate(
    key: string,
    value: unknown,
    dispose: ((v: unknown) => unknown) | undefined,
    request: PendingRequest,
  ): void {
    this.#scheduler.post(() => {
      let err: unknown = null;
      try {
        if (dispose) dispose(value);
      } catch (e) {
        err = e;
      }
      this.#ledger.release(value, `${this.id}/${key}`);
      if (err !== null) {
        const error = err instanceof Error ? err : new Error(String(err));
        this.#destroyErrors.push(error);
      }
      if (!request.userSettled) {
        request.userSettled = true;
        request.rejectUser(new ScopeDestroyedError(`resource "${key}" arrived after scope ${this.id} closed; released immediately`));
      }
    });
  }

  /**
   * 从本作用域（或沿父链向上）借用已就绪资源。
   * 子作用域关闭只归还租约，绝不回收资源本体。
   */
  borrow<T = unknown>(key: string): Lease<T> {
    const owner = this.#findOwner(key);
    if (!owner) {
      throw new BorrowError(`no ready resource "${key}" visible from scope ${this.id}`);
    }
    if (owner.status !== "active") {
      throw new BorrowError(`cannot borrow "${key}": owner scope ${owner.id} is ${owner.status}`);
    }
    owner.journal.commit({ type: "borrow", id: key });
    let released = false;
    const entry = owner.#runtime.get(key)!;
    const lease: Lease<T> = {
      key,
      get value() {
        return entry.value as T;
      },
      release: () => {
        if (released) return;
        released = true;
        owner.journal.commit({ type: "return", id: key });
        this.#heldLeases.delete(lease as Lease<unknown>);
      },
    };
    this.#heldLeases.add(lease as Lease<unknown>);
    return lease;
  }

  #findOwner(key: string): Scope | null {
    let scope: Scope | null = this;
    while (scope) {
      const node = scope.journal.state.scope.nodes[key];
      if (node && node.status === "ready") return scope;
      scope = scope.#parent;
    }
    return null;
  }

  /**
   * 销毁作用域：幂等（重复调用返回同一 Promise）。
   * 顺序：先子作用域（逆创建序），再等待在途 acquire 收敛，
   * 最后按依赖图逆序清理本作用域资源；任何失败都聚合后继续。
   */
  destroy(): Promise<void> {
    if (this.#destroyPromise) return this.#destroyPromise;
    this.#destroyPromise = this.#destroyInternal();
    return this.#destroyPromise;
  }

  async #destroyInternal(): Promise<void> {
    // 立即进入 destroying：此后新增 acquire/borrow/child 一律拒绝。
    this.journal.commit({ type: "destroyBegin" });

    // 归还本作用域持有的全部租约（不回收资源本体）。
    for (const lease of [...this.#heldLeases]) lease.release();

    // 子作用域先行销毁（逆创建序）；子级错误聚合进父级。
    const children = [...this.#children].reverse();
    for (const child of children) {
      try {
        await child.destroy();
      } catch (err) {
        if (err instanceof AggregateError) this.#destroyErrors.push(...err.errors);
        else this.#destroyErrors.push(err as Error);
      }
      this.journal.commit({ type: "childDestroyed", childId: child.id });
    }

    // 等待在途 acquire 全部结算（迟到者会被立即归还）。
    while (this.#pendingCount > 0) {
      await this.#scheduler.drain();
      if (this.#pendingCount > 0) await this.#scheduler.waitActivity();
    }
    await this.#scheduler.drain();

    // 依据当前已提交状态计算清理计划（拓扑逆序 + 确定性破环）。
    const plan = computeDisposalPlan(this);
    this.journal.commit({ type: "disposalPlan", order: plan.order });
    for (const members of plan.cycles) {
      this.#destroyErrors.push(new DependencyCycleError(members));
    }

    for (const key of plan.order) {
      await this.#disposeOne(key);
    }

    const errors = this.#destroyErrors;
    this.journal.commit({ type: "destroyEnd", errors: errors.map(serializeError) });
    if (errors.length > 0) {
      throw new AggregateError(errors, `scope ${this.id} destroyed with ${errors.length} error(s)`);
    }
  }

  async #disposeOne(key: string): Promise<void> {
    const entry = this.#runtime.get(key);
    if (!entry) return;
    this.#runtime.delete(key);
    this.journal.commit({ type: "disposeBegin", id: key });

    let err: SerializedError | null = null;
    try {
      if (entry.destroyer) {
        await this.#scheduler.asTask(Promise.resolve().then(() => entry.destroyer!(entry.value as never)));
      }
    } catch (e) {
      err = serializeError(e);
      this.#destroyErrors.push(e instanceof Error ? e : new Error(String(e)));
    } finally {
      // 无论清理是否抛错，物理释放只登记一次（幂等由运行时条目删除保证）。
      this.#ledger.release(entry.value, `${this.id}/${key}`);
      this.journal.commit({ type: "disposeEnd", id: key, error: err });
    }
  }
}

export interface DisposalPlan {
  /** 清理顺序：依赖者先于被依赖者；确定性。 */
  order: string[];
  /** 检测到的环（成员按 key 排序），环内按注册序清理。 */
  cycles: string[][];
}

/**
 * 从作用域已提交状态计算清理计划：
 * 1. 仅考虑 ready 节点；依赖边 dep -> node（dep 先被指向）。
 * 2. Tarjan 求强连通分量；单节点自环也算环。
 * 3. 环成员按注册序输出（确定性破环），非环节点按拓扑逆序。
 */
export function computeDisposalPlan(scope: Scope): DisposalPlan {
  const nodes = scope.journal.state.scope.nodes;
  const ready = Object.values(nodes).filter((n) => n.status === "ready");
  const readyKeys = new Set(ready.map((n) => n.key));
  // 注册序：Object.values 保持插入序。
  const regOrder = new Map(ready.map((n, i) => [n.key, i]));

  const edges = new Map<string, string[]>();
  for (const node of ready) {
    edges.set(
      node.key,
      node.deps.filter((d) => readyKeys.has(d)),
    );
  }

  // Tarjan SCC
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const sccs: string[][] = [];
  let counter = 0;

  const strongconnect = (v: string): void => {
    index.set(v, counter);
    low.set(v, counter);
    counter++;
    stack.push(v);
    onStack.add(v);
    for (const w of edges.get(v) ?? []) {
      if (!index.has(w)) {
        strongconnect(w);
        low.set(v, Math.min(low.get(v)!, low.get(w)!));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v)!, index.get(w)!));
      }
    }
    if (low.get(v) === index.get(v)) {
      const scc: string[] = [];
      let w: string;
      do {
        w = stack.pop()!;
        onStack.delete(w);
        scc.push(w);
      } while (w !== v);
      sccs.push(scc);
    }
  };
  for (const key of [...readyKeys].sort((a, b) => regOrder.get(a)! - regOrder.get(b)!)) {
    if (!index.has(key)) strongconnect(key);
  }

  // 缩点：SCC 间边 scc(u) -> scc(v) 当 u 依赖 v（u 先清理）。
  const sccOf = new Map<string, number>();
  sccs.forEach((scc, i) => scc.forEach((k) => sccOf.set(k, i)));
  const sccEdges = new Map<number, Set<number>>();
  sccs.forEach((_, i) => sccEdges.set(i, new Set()));
  for (const [u, deps] of edges) {
    for (const v of deps) {
      const a = sccOf.get(u)!;
      const b = sccOf.get(v)!;
      if (a !== b) sccEdges.get(a)!.add(b);
    }
  }

  // Kahn 拓扑（确定性 tie-break：同层取注册序最大者，即栈式/LIFO）。
  const sccMinReg = sccs.map((scc) => Math.min(...scc.map((k) => regOrder.get(k)!)));
  const indeg = new Map<number, number>();
  sccs.forEach((_, i) => indeg.set(i, 0));
  for (const targets of sccEdges.values()) {
    for (const t of targets) indeg.set(t, indeg.get(t)! + 1);
  }
  const available = [...indeg.entries()].filter(([, d]) => d === 0).map(([i]) => i);
  const topoSccs: number[] = [];
  while (available.length > 0) {
    available.sort((a, b) => sccMinReg[b] - sccMinReg[a]);
    const next = available.shift()!;
    topoSccs.push(next);
    for (const t of sccEdges.get(next)!) {
      indeg.set(t, indeg.get(t)! - 1);
      if (indeg.get(t) === 0) available.push(t);
    }
  }

  const cycles: string[][] = [];
  const order: string[] = [];
  for (const sccIdx of topoSccs) {
    const scc = sccs[sccIdx];
    const isCycle =
      scc.length > 1 || (scc.length === 1 && (edges.get(scc[0]) ?? []).includes(scc[0]));
    const sorted = [...scc].sort((a, b) => regOrder.get(a)! - regOrder.get(b)!);
    if (isCycle) cycles.push([...sorted].sort());
    order.push(...sorted);
  }
  return { order, cycles };
}
