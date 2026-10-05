/**
 * 事务化状态容器（调试页面多状态分片场景）
 *
 * 设计要点：
 * - 状态是 JSON 兼容的不可变树：只允许 null/boolean/number(有限)/string/数组/普通对象，
 *   所有节点在创建时深冻结，任何版本的历史状态都不会被后续写操作污染。
 * - 一次事务提交产生一个全局单调递增的版本号；分支提交（从旧版本 begin）也只追加新
 *   版本，既有历史永不覆盖。
 * - 持久化结构共享：未修改的子树在新旧版本间共享同一批节点。容量上限按“实际保留的
 *   树节点（对象/数组容器）”计费，引用计数管理；被活动快照固定（pin）的版本不可回收。
 * - 通知：订阅者每个版本最多收到一次通知，且看到的是该版本完整状态。通知是同步
 *   轮次制；回调期间发起的新事务进入队列，排到本轮通知之后。回调抛错被收集，
 *   不影响其他订阅者。
 */

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };
export type Path = ReadonlyArray<string | number>;
export type Listener = (state: JsonObject, version: number) => void;

export class VersionUnavailableError extends Error {
  constructor(version: number) {
    super(`版本 ${version} 不存在或已被容量回收`);
    this.name = "VersionUnavailableError";
  }
}

export class CapacityExceededError extends Error {
  constructor(needed: number, capacity: number) {
    super(`容量不足：提交后至少需保留 ${needed} 个节点，上限为 ${capacity}`);
    this.name = "CapacityExceededError";
  }
}

export class TransactionClosedError extends Error {
  constructor() {
    super("事务已关闭（已提交或已中止）");
    this.name = "TransactionClosedError";
  }
}

/* ---------------- 内部工具 ---------------- */

function isPlainObject(value: unknown): value is JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function isContainer(value: unknown): value is JsonObject | JsonValue[] {
  return typeof value === "object" && value !== null;
}

/** 校验值是 JSON 兼容树：无 undefined/function/symbol/bigint、数字有限、无循环引用。 */
export function assertJsonValue(value: unknown, seen: Set<object> = new Set()): void {
  if (value === null) return;
  switch (typeof value) {
    case "boolean":
    case "string":
      return;
    case "number":
      if (!Number.isFinite(value)) throw new TypeError(`非 JSON 兼容数字: ${value}`);
      return;
    case "object": {
      if (seen.has(value)) throw new TypeError("值存在循环引用，不是 JSON 兼容树");
      seen.add(value);
      if (Array.isArray(value)) {
        for (const item of value) {
          if (item === undefined) throw new TypeError("数组包含 undefined/空洞，不是 JSON 兼容树");
          assertJsonValue(item, seen);
        }
      } else if (isPlainObject(value)) {
        for (const key of Object.keys(value)) assertJsonValue((value as JsonObject)[key], seen);
      } else {
        throw new TypeError("只允许普通对象、数组与 JSON 原始值");
      }
      seen.delete(value);
      return;
    }
    default:
      throw new TypeError(`不支持的类型: ${typeof value}`);
  }
}

/** 深克隆并逐层冻结；克隆后与调用方对象完全脱钩。 */
function freezeClone(value: JsonValue): JsonValue {
  if (Array.isArray(value)) {
    return Object.freeze(value.map(freezeClone)) as unknown as JsonValue;
  }
  if (isPlainObject(value)) {
    const out: JsonObject = {};
    for (const key of Object.keys(value)) setOwn(out, key, freezeClone(value[key]));
    return Object.freeze(out);
  }
  return value;
}

/** 用 defineProperty 写自有属性，避免 "__proto__" 触发原型 setter。 */
function setOwn(target: JsonObject, key: string, value: JsonValue): void {
  Object.defineProperty(target, key, {
    value,
    enumerable: true,
    writable: true,
    configurable: true,
  });
}

function assertPath(path: Path): void {
  if (!Array.isArray(path) || path.length === 0) {
    throw new TypeError("路径必须是非空数组");
  }
  for (const key of path) {
    if (typeof key === "string") {
      if (key === "" || key === "__proto__") throw new TypeError(`非法路径键: ${JSON.stringify(key)}`);
    } else if (typeof key === "number") {
      if (!Number.isInteger(key) || key < 0) throw new TypeError(`非法数组下标: ${key}`);
    } else {
      throw new TypeError(`路径键只能是 string/number: ${String(key)}`);
    }
  }
}

function getChild(node: JsonObject | JsonValue[], key: string | number): JsonValue | undefined {
  if (Array.isArray(node)) return node[key as number];
  if (!Object.prototype.hasOwnProperty.call(node, key)) return undefined;
  return (node as JsonObject)[key as string];
}

/**
 * 取得当前层级容器；缺失时按下一个键的类型自动补建（auto-vivify）。
 * 已存在的基础值（非对象/数组）视为路径冲突。
 */
function materialize(node: JsonValue | undefined, key: string | number): JsonObject | JsonValue[] {
  if (node === undefined) return typeof key === "number" ? [] : {};
  if (Array.isArray(node)) {
    if (typeof key !== "number") throw new TypeError(`数组下标必须是数字，得到 ${JSON.stringify(key)}`);
    return node;
  }
  if (isPlainObject(node)) {
    if (typeof key !== "string") throw new TypeError(`对象键必须是字符串，得到 ${JSON.stringify(key)}`);
    return node;
  }
  throw new TypeError(`路径冲突：${JSON.stringify(node)} 不是对象/数组`);
}

function shallowCopy(node: JsonObject | JsonValue[]): JsonObject | JsonValue[] {
  return Array.isArray(node) ? node.slice() : { ...node };
}

/** 持久化 set：只重建路径上的节点，未触碰的子树整体共享。 */
function assoc(node: JsonValue | undefined, path: Path, index: number, value: JsonValue): JsonValue {
  const key = path[index];
  const base = materialize(node, key);
  if (Array.isArray(base) && (key as number) > base.length) {
    throw new RangeError(`数组下标越界: ${key as number}（长度 ${base.length}，允许追加到 ${base.length}）`);
  }
  const copy = shallowCopy(base);
  if (index === path.length - 1) {
    if (Array.isArray(copy)) copy[key as number] = value;
    else setOwn(copy as JsonObject, key as string, value);
  } else {
    const child = getChild(base, key);
    const newChild = assoc(child, path, index + 1, value);
    if (Array.isArray(copy)) copy[key as number] = newChild;
    else setOwn(copy as JsonObject, key as string, newChild);
  }
  return Object.freeze(copy);
}

/** 持久化 delete：对象删键、数组 splice；路径必须存在。 */
function dissoc(node: JsonValue | undefined, path: Path, index: number): JsonValue {
  const key = path[index];
  if (!isContainer(node)) {
    throw new Error(`删除失败：路径 ${JSON.stringify(path)} 不存在`);
  }
  if (Array.isArray(node)) {
    if (typeof key !== "number" || key >= node.length) {
      throw new Error(`删除失败：数组下标 ${JSON.stringify(key)} 不存在`);
    }
  } else if (isPlainObject(node)) {
    if (typeof key !== "string" || !Object.prototype.hasOwnProperty.call(node, key)) {
      throw new Error(`删除失败：键 ${JSON.stringify(key)} 不存在`);
    }
  } else {
    throw new Error(`删除失败：路径 ${JSON.stringify(path)} 不存在`);
  }
  const copy = shallowCopy(node);
  if (index === path.length - 1) {
    if (Array.isArray(copy)) copy.splice(key as number, 1);
    else delete (copy as JsonObject)[key as string];
  } else {
    const child = getChild(node, key);
    const newChild = dissoc(child, path, index + 1);
    if (Array.isArray(copy)) copy[key as number] = newChild;
    else setOwn(copy as JsonObject, key as string, newChild);
  }
  return Object.freeze(copy);
}

/** 统计从若干根可达的容器节点数（与账本口径一致：只计对象/数组）。 */
function countReachable(roots: JsonValue[]): number {
  const seen = new Set<object>();
  const stack: JsonValue[] = [...roots];
  let count = 0;
  while (stack.length > 0) {
    const node = stack.pop()!;
    if (!isContainer(node) || seen.has(node)) continue;
    seen.add(node);
    count += 1;
    const children: JsonValue[] = Array.isArray(node) ? node : Object.values(node);
    for (const child of children) stack.push(child);
  }
  return count;
}

/* ---------------- 版本历史 ---------------- */

/** @internal */
export interface VersionEntry {
  version: number;
  parent: number | null;
  root: JsonObject;
  /** 快照/检出句柄与待通知队列的固定计数；>0 时不可被容量回收。 */
  pinned: number;
}

export interface Ledger {
  /** 实际保留的树节点数（只计对象/数组容器）。 */
  retainedNodes: number;
  /** 仍在历史中的版本号（未含已被回收的）。 */
  versions: number[];
}

export class Store {
  private versions = new Map<number, VersionEntry>();
  private counter = 0;
  private headVersion = 0;
  private refcounts = new Map<object, number>();
  private retained = 0;
  private listeners = new Set<Listener>();
  private queue: number[] = [];
  private flushing = false;
  /** 订阅回调抛出的错误按顺序收集在这里，不会中断其他订阅者。 */
  readonly listenerErrors: unknown[] = [];
  private readonly capacity: number;

  constructor(initialState: JsonObject = {}, options: { capacity?: number } = {}) {
    this.capacity = options.capacity ?? Number.POSITIVE_INFINITY;
    if (!(this.capacity >= 1)) throw new RangeError("capacity 必须 >= 1");
    assertJsonValue(initialState);
    if (!isPlainObject(initialState)) {
      throw new TypeError("根状态必须是普通对象（分片名 -> 子树）");
    }
    const root = freezeClone(initialState) as JsonObject;
    const entry: VersionEntry = { version: 1, parent: null, root, pinned: 0 };
    this.counter = 1;
    this.versions.set(1, entry);
    this.retain(root);
    this.headVersion = 1;
    if (this.retained > this.capacity) {
      throw new CapacityExceededError(this.retained, this.capacity);
    }
  }

  /** 当前头部版本号。 */
  get version(): number {
    return this.headVersion;
  }

  /** 当前头部完整状态（冻结的不可变树）。 */
  get state(): JsonObject {
    return this.versions.get(this.headVersion)!.root;
  }

  /** 内存账本。 */
  get ledger(): Ledger {
    return { retainedNodes: this.retained, versions: [...this.versions.keys()] };
  }

  hasVersion(version: number): boolean {
    return this.versions.has(version);
  }

  parentOf(version: number): number | null {
    const entry = this.versions.get(version);
    if (!entry) throw new VersionUnavailableError(version);
    return entry.parent;
  }

  /** 从事务基版本（默认当前头部）开启事务；从旧版本开启即创建分支。 */
  begin(options: { from?: number } = {}): Transaction {
    const from = options.from ?? this.headVersion;
    const entry = this.versions.get(from);
    if (!entry) throw new VersionUnavailableError(from);
    return new Transaction(this, entry);
  }

  /** 订阅版本通知；返回取消订阅函数。 */
  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    let active = true;
    return () => {
      if (!active) return;
      active = false;
      this.listeners.delete(listener);
    };
  }

  /** 固定当前头部版本，返回可释放的快照句柄。 */
  snapshot(): Snapshot {
    return this.checkout(this.headVersion);
  }

  /** 读取历史版本（可据此 begin 出分支）；句柄固定该版本直到 release。 */
  checkout(version: number): Snapshot {
    const entry = this.versions.get(version);
    if (!entry) throw new VersionUnavailableError(version);
    return new Snapshot(this, entry);
  }

  /** @internal 测试用：从全部保留版本可达的节点数，应与账本一致。 */
  debugReachableNodes(): number {
    return countReachable([...this.versions.values()].map((entry) => entry.root));
  }

  /* ------------- 供 Transaction/Snapshot 调用的内部方法 ------------- */

  /** @internal */
  retainNode(node: JsonValue): void {
    this.retain(node);
  }

  /** @internal */
  releaseNode(node: JsonValue): void {
    this.release(node);
  }

  /** @internal */
  commitTransaction(tx: Transaction): number {
    const root = tx.workingRoot;
    // 试算：所有被固定的版本 + 新头部，是提交后不可回收的下界。
    const pinnedRoots: JsonValue[] = [];
    for (const entry of this.versions.values()) {
      if (entry.pinned > 0) pinnedRoots.push(entry.root);
    }
    pinnedRoots.push(root);
    const needed = countReachable(pinnedRoots);
    if (needed > this.capacity) {
      this.release(tx.baseRoot);
      throw new CapacityExceededError(needed, this.capacity);
    }
    const version = ++this.counter;
    const entry: VersionEntry = { version, parent: tx.baseVersion, root, pinned: 0 };
    this.versions.set(version, entry);
    this.retain(root);
    this.headVersion = version;
    this.release(tx.baseRoot);
    this.evictIfNeeded();
    this.enqueue(version);
    return version;
  }

  /** @internal */
  abortTransaction(tx: Transaction): void {
    this.release(tx.baseRoot);
  }

  /* ------------------------- 私有实现 ------------------------- */

  private retain(node: JsonValue): void {
    if (!isContainer(node)) return;
    const count = this.refcounts.get(node) ?? 0;
    this.refcounts.set(node, count + 1);
    if (count === 0) {
      this.retained += 1;
      const children: JsonValue[] = Array.isArray(node) ? node : Object.values(node);
      for (const child of children) this.retain(child);
    }
  }

  private release(node: JsonValue): void {
    if (!isContainer(node)) return;
    const count = this.refcounts.get(node);
    if (count === undefined || count <= 0) {
      throw new Error("内部错误：引用计数下溢");
    }
    if (count === 1) {
      this.refcounts.delete(node);
      this.retained -= 1;
      const children: JsonValue[] = Array.isArray(node) ? node : Object.values(node);
      for (const child of children) this.release(child);
    } else {
      this.refcounts.set(node, count - 1);
    }
  }

  /** 容量回收：从最老版本开始，逐出未固定且非头部的版本。 */
  private evictIfNeeded(): void {
    if (this.retained <= this.capacity) return;
    for (const entry of [...this.versions.values()]) {
      if (this.retained <= this.capacity) break;
      if (entry.version === this.headVersion || entry.pinned > 0) continue;
      this.versions.delete(entry.version);
      this.release(entry.root);
    }
  }

  private enqueue(version: number): void {
    const entry = this.versions.get(version)!;
    entry.pinned += 1; // 通知完成前不可回收
    this.queue.push(version);
    this.flush();
  }

  /**
   * 同步轮次制通知：逐版本、按订阅顺序通知；本轮期间提交的新版本排在队尾，
   * 等本轮所有订阅者都收到当前版本后再处理。回调抛错被收集，不影响他人。
   */
  private flush(): void {
    if (this.flushing) return;
    this.flushing = true;
    try {
      while (this.queue.length > 0) {
        const version = this.queue.shift()!;
        const entry = this.versions.get(version)!;
        const state = entry.root;
        for (const listener of [...this.listeners]) {
          if (!this.listeners.has(listener)) continue; // 本轮中已被取消订阅
          try {
            listener(state, version);
          } catch (error) {
            this.listenerErrors.push(error);
          }
        }
        entry.pinned -= 1;
      }
    } finally {
      this.flushing = false;
    }
  }
}

/* ---------------- 事务与快照 ---------------- */

export class Transaction {
  private store: Store;
  /** 事务基版本（分支提交时即父版本）。 */
  readonly baseVersion: number;
  private base: JsonObject;
  private working: JsonObject;
  private closed = false;

  /** @internal 请使用 store.begin() / snapshot.begin()。 */
  constructor(store: Store, entry: VersionEntry) {
    this.store = store;
    this.baseVersion = entry.version;
    this.base = entry.root;
    this.working = entry.root;
    // 事务存活期间基版本节点不可被回收（即使历史条目被逐出）。
    store.retainNode(entry.root);
  }

  /** @internal */
  get baseRoot(): JsonObject {
    return this.base;
  }

  /** @internal */
  get workingRoot(): JsonObject {
    return this.working;
  }

  private assertOpen(): void {
    if (this.closed) throw new TransactionClosedError();
  }

  /** 读取工作区（基版本 + 已暂存的写操作）。 */
  get(path: Path): JsonValue | undefined {
    this.assertOpen();
    assertPath(path);
    let node: JsonValue | undefined = this.working;
    for (const key of path) {
      if (!isContainer(node)) return undefined;
      node = getChild(node, key);
    }
    return node;
  }

  /** 暂存一次写入；值必须是 JSON 兼容树，会被深克隆并冻结。 */
  set(path: Path, value: JsonValue): void {
    this.assertOpen();
    assertPath(path);
    assertJsonValue(value);
    this.working = assoc(this.working, path, 0, freezeClone(value)) as JsonObject;
  }

  /** 暂存一次删除；路径必须已存在。 */
  delete(path: Path): void {
    this.assertOpen();
    assertPath(path);
    this.working = dissoc(this.working, path, 0) as JsonObject;
  }

  /** 提交：产生一个新的全局版本并触发通知；返回新版本号。 */
  commit(): number {
    this.assertOpen();
    this.closed = true;
    return this.store.commitTransaction(this);
  }

  /** 中止：丢弃全部暂存写操作，不产生版本。 */
  abort(): void {
    if (this.closed) return;
    this.closed = true;
    this.store.abortTransaction(this);
  }
}

/** 快照/检出句柄：固定一个版本，可读取、可开分支事务，用毕 release。 */
export class Snapshot {
  private store: Store;
  private entry: VersionEntry | null;

  /** @internal 请使用 store.snapshot() / store.checkout()。 */
  constructor(store: Store, entry: VersionEntry) {
    this.store = store;
    this.entry = entry;
    entry.pinned += 1;
  }

  private mustEntry(): VersionEntry {
    if (!this.entry) throw new Error("快照已释放");
    return this.entry;
  }

  get version(): number {
    return this.mustEntry().version;
  }

  /** 该版本的完整状态（冻结的不可变树）。 */
  get state(): JsonObject {
    return this.mustEntry().root;
  }

  get released(): boolean {
    return this.entry === null;
  }

  /** 从该版本开启分支事务；提交后得到新的全局版本，不改写任何既有历史。 */
  begin(): Transaction {
    return this.store.begin({ from: this.version });
  }

  /** 释放固定；之后该版本可能因容量上限被回收。幂等。 */
  release(): void {
    const entry = this.entry;
    if (!entry) return;
    entry.pinned -= 1;
    this.entry = null;
  }
}
