/**
 * 事务日志：作用域的全部状态变化都表示为可序列化的 Op，
 * 通过纯函数 reduce 提交。提交是原子的——读者只能看到
 * 上一个已提交的快照，永远看不到事务中间态。
 * 全部 op 可重放（replay），从初始状态确定性重建任意版本。
 */

export type NodeStatus = "pending" | "ready" | "canceled" | "failed" | "disposed";
export type ScopeStatus = "active" | "destroying" | "destroyed";

export interface SerializedError {
  name: string;
  message: string;
}

export interface NodeRecord {
  id: string;
  key: string;
  status: NodeStatus;
  deps: readonly string[];
  borrows: number;
  error?: SerializedError;
}

export interface ScopeRecord {
  id: string;
  parentId: string | null;
  status: ScopeStatus;
  nodes: Readonly<Record<string, NodeRecord>>;
  children: readonly string[];
  errors: readonly SerializedError[];
}

export interface ScopeState {
  scope: ScopeRecord;
}

  export type Op =
  | { type: "register"; id: string; key: string; deps: readonly string[] }
  | { type: "link"; id: string; dep: string }
  | { type: "ready"; id: string }
  | { type: "fail"; id: string; error: SerializedError }
  | { type: "cancel"; id: string }
  | { type: "borrow"; id: string }
  | { type: "return"; id: string }
  | { type: "destroyBegin" }
  | { type: "disposalPlan"; order: readonly string[] }
  | { type: "disposeBegin"; id: string }
  | { type: "disposeEnd"; id: string; error: SerializedError | null }
  | { type: "childRegistered"; childId: string }
  | { type: "childDestroyed"; childId: string }
  | { type: "destroyEnd"; errors: readonly SerializedError[] };

export function serializeError(err: unknown): SerializedError {
  if (err instanceof Error) return { name: err.name, message: err.message };
  return { name: "Error", message: String(err) };
}

function initialState(id: string, parentId: string | null): ScopeState {
  return {
    scope: {
      id,
      parentId,
      status: "active",
      nodes: {},
      children: [],
      errors: [],
    },
  };
}

function cloneState(state: ScopeState): ScopeState {
  const nodes: Record<string, NodeRecord> = {};
  for (const [id, node] of Object.entries(state.scope.nodes)) {
    nodes[id] = { ...node, deps: [...node.deps] };
  }
  return {
    scope: {
      ...state.scope,
      nodes,
      children: [...state.scope.children],
      errors: [...state.scope.errors],
    },
  };
}

/** 纯函数：把单个 op 应用到状态的可变草稿上。 */
export function reduce(draft: ScopeState, op: Op): void {
  const scope = draft.scope;
  switch (op.type) {
    case "register": {
      scope.nodes[op.id] = {
        id: op.id,
        key: op.key,
        status: "pending",
        deps: [...op.deps],
        borrows: 0,
      };
      break;
    }
    case "link": {
      const node = scope.nodes[op.id];
      if (node && !node.deps.includes(op.dep)) {
        node.deps = [...node.deps, op.dep];
      }
      break;
    }
    case "ready": {
      const node = scope.nodes[op.id];
      if (node) node.status = "ready";
      break;
    }
    case "fail": {
      const node = scope.nodes[op.id];
      if (node) {
        node.status = "failed";
        node.error = op.error;
      }
      break;
    }
    case "cancel": {
      const node = scope.nodes[op.id];
      if (node && node.status === "pending") node.status = "canceled";
      break;
    }
    case "borrow": {
      const node = scope.nodes[op.id];
      if (node) node.borrows++;
      break;
    }
    case "return": {
      const node = scope.nodes[op.id];
      if (node && node.borrows > 0) node.borrows--;
      break;
    }
    case "destroyBegin": {
      scope.status = "destroying";
      break;
    }
    case "disposalPlan": {
      break;
    }
    case "disposeBegin": {
      break;
    }
    case "disposeEnd": {
      const node = scope.nodes[op.id];
      if (node) {
        node.status = "disposed";
        if (op.error) scope.errors = [...scope.errors, op.error];
      }
      break;
    }
    case "childRegistered": {
      scope.children = [...scope.children, op.childId];
      break;
    }
    case "childDestroyed": {
      scope.children = scope.children.filter((c) => c !== op.childId);
      break;
    }
    case "destroyEnd": {
      scope.status = "destroyed";
      scope.errors = [...scope.errors, ...op.errors];
      break;
    }
  }
}

function deepFreeze<T>(value: T, seen = new Set<object>()): T {
  if (value !== null && typeof value === "object" && !seen.has(value)) {
    seen.add(value);
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key], seen);
    }
    Object.freeze(value);
  }
  return value;
}

export class Journal {
  #state: ScopeState;
  readonly #ops: Op[] = [];
  readonly #id: string;
  readonly #parentId: string | null;

  constructor(id: string, parentId: string | null) {
    this.#id = id;
    this.#parentId = parentId;
    this.#state = deepFreeze(initialState(id, parentId));
  }

  /** 当前已提交快照（深冻结，读者无法看到或制造中间态）。 */
  get state(): ScopeState {
    return this.#state;
  }

  /** 已提交 op 的只读副本。 */
  get ops(): readonly Op[] {
    return [...this.#ops];
  }

  /** 原子提交：先对草稿应用全部 op，全部成功后一次性换入。 */
  commit(...ops: Op[]): void {
    const draft = cloneState(this.#state);
    for (const op of ops) reduce(draft, op);
    this.#state = deepFreeze(draft);
    this.#ops.push(...ops);
  }

  /** 从初始状态重放全部 op，必须得到与当前一致的状态。 */
  verifyReplay(): boolean {
    const replayed = replay(this.#id, this.#parentId, this.#ops);
    return JSON.stringify(replayed) === JSON.stringify(this.#state);
  }
}

/** 独立重放：给定初始参数与 op 序列，重建状态。 */
export function replay(id: string, parentId: string | null, ops: readonly Op[]): ScopeState {
  const state = initialState(id, parentId);
  for (const op of ops) reduce(state, op);
  return deepFreeze(state);
}
