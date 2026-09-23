/**
 * 乐观并发状态仓库（零依赖、零框架）。
 *
 * 设计要点：
 * - 每个操作携带一个原子补丁（一组操作步骤，全部生效或全部不生效）。
 * - submit 先把补丁应用到乐观视图，按提交顺序进入待确认队列；
 *   已提交快照始终只包含已确认操作，读者永远看不到中间态/未确认态。
 * - confirm 必须同时匹配操作 ID 与该操作登记时所基于的已提交版本，
 *   重复确认幂等；迟到的旧响应（版本不匹配）不会回退新状态。
 * - reject 只移除/丢弃该操作自身：以已确认日志为基准重放其余操作，
 *   因此 A 乐观写之后 B 的写入不会被 A 的失败覆盖。
 * - 所有已确认状态都是纯函数日志的折叠结果，可随时回放重建。
 */

export type Json =
  | string
  | number
  | boolean
  | null
  | Json[]
  | { [key: string]: Json };

export type StoreState = { [key: string]: Json };
export type OpId = string;
export type Segment = string | number;

export type Patch =
  | { readonly op: "set"; readonly path: readonly Segment[]; readonly value: Json }
  | { readonly op: "remove"; readonly path: readonly Segment[] }
  | { readonly op: "add"; readonly path: readonly Segment[]; readonly delta: number };

export type Status = "pending" | "committed" | "rejected" | "conflicted";

export interface LogEntry {
  readonly id: OpId;
  readonly baseVersion: number;
  readonly patch: readonly Patch[];
}

export interface Snapshot {
  readonly version: number;
  readonly state: StoreState;
}

/** 补丁无法原子应用（路径不存在、类型不符等）。抛出前后状态保持不变。 */
export class AtomicPatchError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "AtomicPatchError";
  }
}

type Container = StoreState | Json[];

function clone<T>(value: T): T {
  return structuredClone(value);
}

function isObject(value: Json | undefined): value is { [key: string]: Json } {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** 找到路径倒数第二层容器并返回它与最后一段；找不到则抛错（set 时自动建中间层）。 */
function locateParent(
  root: Container,
  path: readonly Segment[],
  autoCreate: boolean,
): { parent: Container; last: Segment } {
  if (path.length === 0) {
    throw new AtomicPatchError("路径不能为空");
  }
  let current: Container = root;
  for (let i = 0; i < path.length - 1; i++) {
    const key = path[i] as Segment;
    if (Array.isArray(current)) {
      if (typeof key !== "number" || !Number.isInteger(key) || key < 0 || key >= current.length) {
        throw new AtomicPatchError(`数组下标无效: ${String(key)}`);
      }
      const next = current[key] as Json | undefined;
      if (!isObject(next) && !(Array.isArray(next))) {
        if (!autoCreate || next !== undefined) {
          throw new AtomicPatchError(`路径在 [${String(key)}] 处不可继续`);
        }
        const nextKey = path[i + 1] as Segment;
        const created: Container = typeof nextKey === "number" ? [] : {};
        current[key] = created as Json;
        current = created;
        continue;
      }
      current = next;
    } else {
      if (typeof key !== "string") {
        throw new AtomicPatchError(`对象键必须是字符串: ${String(key)}`);
      }
      const next = current[key];
      if (!isObject(next) && !Array.isArray(next)) {
        if (!autoCreate || next !== undefined) {
          throw new AtomicPatchError(`路径在 [${key}] 处不可继续`);
        }
        const nextKey = path[i + 1] as Segment;
        const created: Container = typeof nextKey === "number" ? [] : {};
        current[key] = created as Json;
        current = created;
        continue;
      }
      current = next;
    }
  }
  return { parent: current, last: path[path.length - 1] as Segment };
}

function applyOne(root: Container, patch: Patch): void {
  switch (patch.op) {
    case "set": {
      const { parent, last } = locateParent(root, patch.path, true);
      if (Array.isArray(parent)) {
        if (typeof last !== "number" || !Number.isInteger(last) || last < 0) {
          throw new AtomicPatchError(`数组下标无效: ${String(last)}`);
        }
        parent[last] = clone(patch.value);
      } else {
        if (typeof last !== "string") {
          throw new AtomicPatchError(`对象键必须是字符串: ${String(last)}`);
        }
        parent[last] = clone(patch.value);
      }
      return;
    }
    case "remove": {
      const { parent, last } = locateParent(root, patch.path, false);
      if (Array.isArray(parent)) {
        if (typeof last !== "number" || !Number.isInteger(last) || last < 0 || last >= parent.length) {
          throw new AtomicPatchError(`数组下标无效: ${String(last)}`);
        }
        parent.splice(last, 1);
      } else {
        if (typeof last !== "string" || !(last in parent)) {
          throw new AtomicPatchError(`删除目标不存在: ${String(last)}`);
        }
        delete parent[last];
      }
      return;
    }
    case "add": {
      const { parent, last } = locateParent(root, patch.path, false);
      if (Array.isArray(parent) || typeof last !== "string") {
        throw new AtomicPatchError("add 仅支持对象上的数值字段");
      }
      const currentValue = parent[last];
      if (typeof currentValue !== "number") {
        throw new AtomicPatchError(`add 目标不是数值: ${String(last)}`);
      }
      parent[last] = currentValue + patch.delta;
      return;
    }
  }
}

/**
 * 原子地把一整个补丁应用到 state，返回新状态。
 * 任一步骤失败都抛出 AtomicPatchError，入参/已有状态不发生任何变化。
 */
export function applyPatch(state: StoreState, patch: readonly Patch[]): StoreState {
  if (patch.length === 0) {
    throw new AtomicPatchError("补丁不能为空");
  }
  const scratch = clone(state);
  for (const step of patch) {
    applyOne(scratch, step);
  }
  return scratch;
}

/** 从初始状态折叠已确认日志重建状态：纯函数，可随时回放。 */
export function replay(initial: StoreState, log: readonly LogEntry[]): StoreState {
  let state = clone(initial);
  for (const entry of log) {
    state = applyPatch(state, entry.patch);
  }
  return state;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object") {
    for (const key of Reflect.ownKeys(value as object)) {
      const child = (value as Record<PropertyKey, unknown>)[key as PropertyKey];
      if (child !== null && typeof child === "object") {
        deepFreeze(child);
      }
    }
    Object.freeze(value);
  }
  return value;
}

export interface SubmitResult {
  readonly id: OpId;
  readonly baseVersion: number;
  readonly view: StoreState;
}

export type ConfirmResult =
  | { readonly kind: "committed"; readonly id: OpId; readonly version: number; readonly state: StoreState }
  | { readonly kind: "already_committed"; readonly id: OpId; readonly version: number; readonly state: StoreState }
  | { readonly kind: "pending"; readonly id: OpId; readonly blockedBy: readonly OpId[] }
  | { readonly kind: "conflicted"; readonly id: OpId; readonly reason: string; readonly state: StoreState }
  | { readonly kind: "unknown"; readonly id: OpId; readonly state: StoreState };

export type RejectResult =
  | { readonly kind: "rejected"; readonly id: OpId; readonly state: StoreState }
  | { readonly kind: "already_rejected"; readonly id: OpId; readonly state: StoreState }
  | { readonly kind: "already_committed"; readonly id: OpId; readonly version: number; readonly state: StoreState }
  | { readonly kind: "unknown"; readonly id: OpId; readonly state: StoreState };

interface InternalEntry {
  readonly id: OpId;
  readonly baseVersion: number;
  readonly patch: readonly Patch[];
  status: Status;
}

export class OptimisticStore {
  private readonly initial: StoreState;
  private committed: StoreState;
  private readonly logEntries: LogEntry[] = [];
  private readonly pending: InternalEntry[] = [];
  private readonly all = new Map<OpId, InternalEntry>();

  constructor(initial: StoreState = {}) {
    this.initial = clone(initial);
    this.committed = clone(initial);
  }

  /** 已提交版本（= 已确认日志条数）。 */
  get version(): number {
    return this.logEntries.length;
  }

  /** 读者视角：只包含已确认状态，任何进行中的事务都不可见。 */
  read(): StoreState {
    return deepFreeze(clone(this.committed));
  }

  snapshot(): Snapshot {
    return deepFreeze({ version: this.version, state: clone(this.committed) });
  }

  /** 提交者视角：在已确认状态之上叠加全部待确认操作后的乐观视图。 */
  view(): StoreState {
    let state = clone(this.committed);
    for (const entry of this.pending) {
      state = applyPatch(state, entry.patch);
    }
    return deepFreeze(state);
  }

  pendingIds(): readonly OpId[] {
    return this.pending.map((entry) => entry.id);
  }

  /**
   * 登记一个乐观操作。baseVersion 必须等于当前已提交版本，
   * 补丁会立刻在乐观视图生效；非法补丁整体不生效并抛出 AtomicPatchError。
   */
  submit(id: OpId, baseVersion: number, patch: readonly Patch[]): SubmitResult {
    if (this.all.has(id)) {
      throw new AtomicPatchError(`重复的操作 ID: ${id}`);
    }
    if (baseVersion !== this.version) {
      throw new AtomicPatchError(
        `baseVersion 过期: 收到 ${baseVersion}，当前已提交版本 ${this.version}`,
      );
    }
    // 先在“已提交 + 已排队”视图上试算，非法补丁原子失败且不留下任何痕迹。
    applyPatch(this.view(), patch);

    const entry: InternalEntry = {
      id,
      baseVersion,
      patch: clone(patch),
      status: "pending",
    };
    this.pending.push(entry);
    this.all.set(id, entry);
    return { id, baseVersion, view: this.view() };
  }

  /**
   * 服务端确认。必须同时匹配操作 ID 与登记时的 baseVersion：
   * - 匹配且排队可提交 => 原子进入已确认日志；
   * - 已提交（重复 ACK）=> 幂等无副作用；
   * - 前面仍有未决操作 => 保持排队，返回阻塞者；
   * - 版本不匹配（迟到的旧响应）=> 忽略，绝不回退状态；
   * - 未知 ID => 忽略。
   */
  confirm(id: OpId, ackBaseVersion: number): ConfirmResult {
    const entry = this.all.get(id);
    if (!entry) {
      return { kind: "unknown", id, state: this.read() };
    }
    if (entry.baseVersion !== ackBaseVersion) {
      return { kind: "conflicted", id, reason: "ack 版本与登记版本不一致（迟到的旧响应，已忽略）", state: this.read() };
    }
    if (entry.status === "committed") {
      return { kind: "already_committed", id, version: this.version, state: this.read() };
    }
    if (entry.status === "rejected" || entry.status === "conflicted") {
      return { kind: "conflicted", id, reason: `操作此前已被标记为 ${entry.status}`, state: this.read() };
    }

    const blockedBy = this.blockedBy(entry);
    if (blockedBy.length > 0) {
      return { kind: "pending", id, blockedBy };
    }
    this.pump();
    return entry.status === "committed"
      ? { kind: "committed", id, version: this.version, state: this.read() }
      : { kind: "conflicted", id, reason: "提交时补丁与已确认状态冲突", state: this.read() };
  }

  /**
   * 服务端拒绝/操作失败：仅丢弃该操作自身。
   * 已确认日志不动，状态以日志为准重放；排队中其余操作继续保留。
   */
  reject(id: OpId): RejectResult {
    const entry = this.all.get(id);
    if (!entry) {
      return { kind: "unknown", id, state: this.read() };
    }
    if (entry.status === "committed") {
      return { kind: "already_committed", id, version: this.version, state: this.read() };
    }
    if (entry.status === "rejected" || entry.status === "conflicted") {
      return { kind: "already_rejected", id, state: this.read() };
    }
    entry.status = "rejected";
    this.pump();
    return { kind: "rejected", id, state: this.read() };
  }

  /** 已确认日志（顺序即提交顺序），可据此持久化/回放。 */
  log(): readonly LogEntry[] {
    return this.logEntries.map((entry) => clone(entry));
  }

  /** 从日志重建一个等价仓库。 */
  static hydrate(initial: StoreState, log: readonly LogEntry[]): OptimisticStore {
    const store = new OptimisticStore(initial);
    store.committed = replay(initial, log);
    store.logEntries.push(...clone(log));
    return store;
  }

  private blockedBy(entry: InternalEntry): readonly OpId[] {
    const blockers: OpId[] = [];
    for (const queued of this.pending) {
      if (queued === entry) {
        break;
      }
      if (queued.status === "pending") {
        blockers.push(queued.id);
      }
    }
    return blockers;
  }

  /**
   * 从队首开始推进：已拒绝的跳过；未决但无法在最新已确认状态上
   * 原子应用的（排队依赖的前序变化破坏了路径）标记冲突并跳过；
   * 其余按序提交。每次提交都是整体替换状态快照，读者看不到中间态。
   */
  private pump(): void {
    while (this.pending.length > 0) {
      const entry = this.pending[0] as InternalEntry;
      if (entry.status === "rejected" || entry.status === "conflicted") {
        this.pending.shift();
        continue;
      }
      if (entry.status === "committed") {
        this.pending.shift();
        continue;
      }
      let next: StoreState;
      try {
        next = applyPatch(this.committed, entry.patch);
      } catch {
        entry.status = "conflicted";
        this.pending.shift();
        continue;
      }
      this.committed = next;
      this.logEntries.push({ id: entry.id, baseVersion: entry.baseVersion, patch: entry.patch });
      entry.status = "committed";
      this.pending.shift();
    }
  }
}
