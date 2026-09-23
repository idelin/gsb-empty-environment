/**
 * OptimisticStore — 乐观并发存储（零依赖，事件溯源）。
 *
 * 核心机制：
 * 1. 状态 = 基础快照 + 操作日志重放。每个操作携带 opId + version + 变更集。
 * 2. applyOptimistic 先把操作追加到日志并立即重新发布快照（乐观生效）。
 * 3. confirm / reject 按 (opId, version) 精确匹配：
 *    - 重复确认幂等；已终结的操作上的迟到响应一律忽略；
 *    - reject 通过"重放时跳过该操作"实现，只撤销自身效果，
 *      之后其他操作的写入（如 B 覆盖 A 写过的字段）原样保留。
 * 4. 读者只能拿到发布后的不可变快照；transact 内的多步修改一次性发布，
 *    任何时刻都观察不到事务中间态。
 */

export type PathSegment = string | number;
export type Path = readonly PathSegment[];

export type Mutation =
  | { readonly kind: 'set'; readonly path: Path; readonly value: unknown }
  | { readonly kind: 'delete'; readonly path: Path };

export type OpStatus = 'pending' | 'confirmed' | 'rejected';

export interface OperationRecord {
  readonly opId: string;
  readonly version: number;
  readonly mutations: readonly Mutation[];
  readonly seq: number;
  /** 以下两个字段由 store 在终结操作时内部更新；对外仅通过快照副本暴露。 */
  status: OpStatus;
  /** 终结（confirm/reject）发生时的逻辑序号；pending 时为 null。 */
  resolvedSeq: number | null;
}

export type ResolveResult =
  | { readonly applied: true; readonly opId: string; readonly status: OpStatus }
  | {
      readonly applied: false;
      readonly opId: string;
      readonly reason: 'unknown-op' | 'version-mismatch' | 'already-resolved';
      readonly currentStatus?: OpStatus;
      readonly currentVersion?: number;
    };

export interface Transaction {
  applyOptimistic(opId: string, version: number, mutations: readonly Mutation[]): void;
}

export type Listener<S> = (snapshot: Readonly<S>) => void;

function deepFreeze<T>(value: T, seen = new Set<object>()): T {
  if (value !== null && typeof value === 'object' && !seen.has(value)) {
    seen.add(value);
    for (const key of Reflect.ownKeys(value)) {
      deepFreeze((value as Record<PropertyKey, unknown>)[key], seen);
    }
    Object.freeze(value);
  }
  return value;
}

function applyMutation(doc: Record<PropertyKey, unknown>, mutation: Mutation): void {
  const path = mutation.path;
  if (path.length === 0) throw new Error('mutation path must not be empty');
  let node: Record<PropertyKey, unknown> = doc;
  for (let i = 0; i < path.length - 1; i += 1) {
    const key = path[i];
    const nextKey = path[i + 1];
    let child = node[key];
    if (child === null || typeof child !== 'object') {
      child = typeof nextKey === 'number' ? [] : {};
      node[key] = child;
    }
    node = child as Record<PropertyKey, unknown>;
  }
  const last = path[path.length - 1];
  if (mutation.kind === 'set') {
    node[last] = structuredClone(mutation.value);
  } else {
    delete node[last];
  }
}

/** 从基础状态重放操作日志（跳过已撤销的操作），可指定重放到某个 seq。 */
function materialize<S extends object>(
  base: S,
  log: readonly OperationRecord[],
  uptoSeq = Number.POSITIVE_INFINITY,
): S {
  const doc = structuredClone(base) as Record<PropertyKey, unknown>;
  for (const record of log) {
    if (record.seq > uptoSeq) break;
    const rejectedByThen =
      record.status === 'rejected' && (record.resolvedSeq ?? Number.POSITIVE_INFINITY) <= uptoSeq;
    if (rejectedByThen) continue;
    for (const mutation of record.mutations) applyMutation(doc, mutation);
  }
  return doc as S;
}

export class OptimisticStore<S extends object> {
  #base: S;
  #log: OperationRecord[] = [];
  #nextSeq = 1;
  #snapshot: Readonly<S>;
  #listeners = new Set<Listener<S>>();

  constructor(base: S) {
    this.#base = deepFreeze(structuredClone(base));
    this.#snapshot = deepFreeze(structuredClone(base));
  }

  /** 读者入口：永远是上一次完整发布后的不可变快照。 */
  get state(): Readonly<S> {
    return this.#snapshot;
  }

  /** 只读操作日志，供审计与重放。 */
  get log(): readonly OperationRecord[] {
    return this.#log.map((record) => Object.freeze({ ...record }));
  }

  subscribe(listener: Listener<S>): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  /** 乐观应用：先入账、立即生效，等待服务端 confirm / reject。 */
  applyOptimistic(opId: string, version: number, mutations: readonly Mutation[]): OperationRecord {
    const record = this.#stage(opId, version, mutations, this.#log);
    this.#log.push(record);
    this.#publish();
    return Object.freeze({ ...record });
  }

  /** 多步事务：全部成功才一次性发布；抛错则什么都不发布。 */
  transact(fn: (tx: Transaction) => void): void {
    const staged: OperationRecord[] = [];
    const tx: Transaction = {
      applyOptimistic: (opId, version, mutations) => {
        staged.push(this.#stage(opId, version, mutations, this.#log.concat(staged)));
      },
    };
    fn(tx);
    if (staged.length === 0) return;
    for (const record of staged) this.#log.push(record);
    this.#publish();
  }

  /** 服务端确认：按 (opId, version) 匹配；重复确认幂等。 */
  confirm(opId: string, version: number): ResolveResult {
    return this.#resolve(opId, version, 'confirmed');
  }

  /** 服务端拒绝：撤销该操作自身效果，保留其后的所有写入。 */
  reject(opId: string, version: number): ResolveResult {
    return this.#resolve(opId, version, 'rejected');
  }

  /** 时间旅行：重放到指定 seq 的状态（不改当前状态）。 */
  replayTo(seq: number): Readonly<S> {
    return deepFreeze(materialize(this.#base, this.#log, seq));
  }

  /** 把已确认操作折叠进基础状态，日志只保留 pending / rejected。 */
  compact(): void {
    const confirmed = this.#log.filter((record) => record.status === 'confirmed');
    this.#base = deepFreeze(materialize(this.#base, confirmed));
    this.#log = this.#log.filter((record) => record.status !== 'confirmed');
    this.#publish();
  }

  /** 离线重放：从基础状态 + 日志重建状态，验证可回放性。 */
  static replay<S extends object>(base: S, log: readonly OperationRecord[]): Readonly<S> {
    return deepFreeze(materialize(base, log));
  }

  #stage(
    opId: string,
    version: number,
    mutations: readonly Mutation[],
    existing: readonly OperationRecord[],
  ): OperationRecord {
    const duplicate = existing.find((record) => record.opId === opId);
    if (duplicate && duplicate.status === 'pending') {
      throw new Error(`op "${opId}" is still pending; resolve it before reusing the id`);
    }
    return {
      opId,
      version,
      mutations: structuredClone(mutations),
      status: 'pending',
      seq: this.#nextSeq++,
      resolvedSeq: null,
    };
  }

  #resolve(opId: string, version: number, status: 'confirmed' | 'rejected'): ResolveResult {
    // 同一 opId 重试会留下多条记录，响应一律与当前 pending 的那条匹配。
    const pending = this.#log.find((entry) => entry.opId === opId && entry.status === 'pending');
    if (!pending) {
      const resolved = this.#log.findLast((entry) => entry.opId === opId);
      if (!resolved) return { applied: false, opId, reason: 'unknown-op' };
      // 重复确认 / 迟到响应：状态不变，幂等忽略。
      return { applied: false, opId, reason: 'already-resolved', currentStatus: resolved.status };
    }
    if (pending.version !== version) {
      return { applied: false, opId, reason: 'version-mismatch', currentVersion: pending.version };
    }
    pending.status = status;
    pending.resolvedSeq = this.#nextSeq++;
    this.#publish();
    return { applied: true, opId, status };
  }

  #publish(): void {
    this.#snapshot = deepFreeze(materialize(this.#base, this.#log));
    for (const listener of this.#listeners) listener(this.#snapshot);
  }
}
