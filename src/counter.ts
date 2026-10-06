/**
 * 多副本计数器（CRDT，操作集语义）
 *
 * - increment 产生带唯一 id 的操作；每个操作贡献 amount。
 * - reset 产生一个操作，显式携带它“观察到”的递增操作 id 集合，
 *   只清除这些贡献；未被观察到的并发递增在合并后仍然保留。
 * - 合并按操作 id 去重（并集），满足交换律、结合律、幂等律；
 *   不依赖任何本地时钟或时间戳来判断先后。
 */

export type OpId = string;

export interface IncrementOp {
  readonly kind: 'inc';
  readonly id: OpId;
  readonly amount: number;
}

export interface ResetOp {
  readonly kind: 'reset';
  readonly id: OpId;
  /** 该 reset 观察到并清除的递增操作 id 集合 */
  readonly observed: ReadonlyArray<OpId>;
}

export type CounterOp = IncrementOp | ResetOp;

export interface CounterState {
  readonly increments: ReadonlyMap<OpId, IncrementOp>;
  readonly resets: ReadonlyMap<OpId, ResetOp>;
}

export function emptyState(): CounterState {
  return { increments: new Map(), resets: new Map() };
}

/** 当前值 = 未被任何 reset 观察到的递增操作贡献之和 */
export function valueOf(state: CounterState): number {
  const cleared: Set<OpId> = new Set();
  for (const reset of state.resets.values()) {
    for (const id of reset.observed) {
      cleared.add(id);
    }
  }
  let total = 0;
  for (const inc of state.increments.values()) {
    if (!cleared.has(inc.id)) {
      total += inc.amount;
    }
  }
  return total;
}

/**
 * 把单个操作应用到状态上，返回新状态。
 * 按操作 id 去重：重复应用同一操作是空操作（幂等）。
 * 若 reset 观察到的递增尚未到达，之后到达时仍会被清除（集合语义）。
 */
export function applyOp(state: CounterState, op: CounterOp): CounterState {
  if (op.kind === 'inc') {
    if (state.increments.has(op.id)) {
      return state;
    }
    const increments: Map<OpId, IncrementOp> = new Map(state.increments);
    increments.set(op.id, op);
    return { increments, resets: state.resets };
  }
  if (state.resets.has(op.id)) {
    return state;
  }
  const resets: Map<OpId, ResetOp> = new Map(state.resets);
  resets.set(op.id, op);
  return { increments: state.increments, resets };
}

/** 合并两个状态：两个操作集分别按 id 取并集。 */
export function mergeStates(a: CounterState, b: CounterState): CounterState {
  const increments: Map<OpId, IncrementOp> = new Map(a.increments);
  for (const [id, op] of b.increments) {
    if (!increments.has(id)) {
      increments.set(id, op);
    }
  }
  const resets: Map<OpId, ResetOp> = new Map(a.resets);
  for (const [id, op] of b.resets) {
    if (!resets.has(id)) {
      resets.set(id, op);
    }
  }
  return { increments, resets };
}

export interface SerializedState {
  increments: Array<IncrementOp>;
  resets: Array<ResetOp>;
}

export function serializeState(state: CounterState): SerializedState {
  return {
    increments: Array.from(state.increments.values()),
    resets: Array.from(state.resets.values()),
  };
}

export function deserializeState(data: SerializedState): CounterState {
  let state: CounterState = emptyState();
  for (const op of data.increments) {
    state = applyOp(state, op);
  }
  for (const op of data.resets) {
    state = applyOp(state, op);
  }
  return state;
}

/** 状态规范化表示（排序后），用于测试中等价性比较。 */
export function canonicalState(state: CounterState): SerializedState {
  const data: SerializedState = serializeState(state);
  data.increments.sort((x: IncrementOp, y: IncrementOp) => (x.id < y.id ? -1 : 1));
  data.resets.sort((x: ResetOp, y: ResetOp) => (x.id < y.id ? -1 : 1));
  return data;
}

/**
 * 单个副本。本地操作立即生效并作为“消息”返回，
 * 由传输层以任意顺序、任意次数投递到其他副本。
 */
export class CounterReplica {
  readonly replicaId: string;
  private state: CounterState;
  private seq: number;

  constructor(replicaId: string, state?: CounterState) {
    this.replicaId = replicaId;
    this.state = state !== undefined ? state : emptyState();
    this.seq = 0;
  }

  private nextOpId(): OpId {
    this.seq += 1;
    return this.replicaId + ':' + String(this.seq);
  }

  increment(amount?: number): IncrementOp {
    const op: IncrementOp = {
      kind: 'inc',
      id: this.nextOpId(),
      amount: amount !== undefined ? amount : 1,
    };
    this.state = applyOp(this.state, op);
    return op;
  }

  /** 重置：显式记录当前观察到的递增操作集合，只清除这些贡献。 */
  reset(): ResetOp {
    const op: ResetOp = {
      kind: 'reset',
      id: this.nextOpId(),
      observed: Array.from(this.state.increments.keys()),
    };
    this.state = applyOp(this.state, op);
    return op;
  }

  /** 接收远端操作；重复投递安全。返回该操作是否为新操作。 */
  receive(op: CounterOp): boolean {
    const known: boolean =
      op.kind === 'inc' ? this.state.increments.has(op.id) : this.state.resets.has(op.id);
    this.state = applyOp(this.state, op);
    return !known;
  }

  /** 与远端快照合并。 */
  merge(remote: CounterState): void {
    this.state = mergeStates(this.state, remote);
  }

  value(): number {
    return valueOf(this.state);
  }

  snapshot(): CounterState {
    return this.state;
  }
}
