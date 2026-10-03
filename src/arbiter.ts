export type Release = () => void;

export class AsyncMutex {
  private locked = false;
  private queue: Array<() => void> = [];

  get isLocked(): boolean {
    return this.locked;
  }

  get waiters(): number {
    return this.queue.length;
  }

  acquire(): Promise<Release> {
    if (!this.locked) {
      this.locked = true;
      return Promise.resolve(this.makeRelease());
    }
    return new Promise<Release>((resolve) => {
      this.queue.push(() => resolve(this.makeRelease()));
    });
  }

  private makeRelease(): Release {
    let released = false;
    return () => {
      if (released) {
        throw new Error('mutex released twice');
      }
      released = true;
      const next = this.queue.shift();
      if (next === undefined) {
        this.locked = false;
      } else {
        next();
      }
    };
  }

  async withLock<T>(fn: () => T | Promise<T>): Promise<T> {
    const release = await this.acquire();
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

export class KeyedLockManager {
  private locks = new Map<string, AsyncMutex>();

  private lockFor(key: string): AsyncMutex {
    let lock = this.locks.get(key);
    if (lock === undefined) {
      lock = new AsyncMutex();
      this.locks.set(key, lock);
    }
    return lock;
  }

  get heldCount(): number {
    let n = 0;
    for (const lock of this.locks.values()) if (lock.isLocked) n++;
    return n;
  }

  get waiterCount(): number {
    let n = 0;
    for (const lock of this.locks.values()) n += lock.waiters;
    return n;
  }

  async withLocks<T>(keys: readonly string[], fn: () => T | Promise<T>): Promise<T> {
    const ordered = [...new Set(keys)].sort();
    const releases: Release[] = [];
    try {
      for (const key of ordered) {
        releases.push(await this.lockFor(key).acquire());
      }
      return await fn();
    } finally {
      for (let i = releases.length - 1; i >= 0; i--) {
        releases[i]();
      }
    }
  }
}

export type Value = unknown;

export interface ReadOp {
  kind: 'read';
  key: string;
}

export interface WriteOp {
  kind: 'write';
  key: string;
  value: Value;
}

export interface CasOp {
  kind: 'cas';
  key: string;
  expectedVersion: bigint;
  value: Value;
}

export type Operation = ReadOp | WriteOp | CasOp;

export interface OpResult {
  ok: boolean;
  conflict: boolean;
  version: bigint;
  value: Value;
}

export interface HistoryEntry {
  seq: number;
  nodeId: string;
  op: Operation;
  result: OpResult;
  invokedAt: number;
  respondedAt: number;
}

interface Cell {
  version: bigint;
  value: Value;
}

export class Arbiter {
  private readonly state = new Map<string, Cell>();
  private readonly mutex = new AsyncMutex();
  private clock = 0;
  private seq = 0;
  readonly history: HistoryEntry[] = [];

  get pendingCount(): number {
    return this.mutex.waiters + (this.mutex.isLocked ? 1 : 0);
  }

  versionOf(key: string): bigint {
    return this.state.get(key)?.version ?? 0n;
  }

  valueOf(key: string): Value {
    return this.state.get(key)?.value;
  }

  private cell(key: string): Cell {
    let c = this.state.get(key);
    if (c === undefined) {
      c = { version: 0n, value: undefined };
      this.state.set(key, c);
    }
    return c;
  }

  private apply(op: Operation): OpResult {
    const c = this.cell(op.key);
    switch (op.kind) {
      case 'read':
        return { ok: true, conflict: false, version: c.version, value: c.value };
      case 'write':
        c.version += 1n;
        c.value = op.value;
        return { ok: true, conflict: false, version: c.version, value: c.value };
      case 'cas':
        if (c.version !== op.expectedVersion) {
          return { ok: false, conflict: true, version: c.version, value: c.value };
        }
        c.version += 1n;
        c.value = op.value;
        return { ok: true, conflict: false, version: c.version, value: c.value };
    }
  }

  async execute(nodeId: string, op: Operation): Promise<OpResult> {
    const invokedAt = ++this.clock;
    const release = await this.mutex.acquire();
    try {
      const result = this.apply(op);
      this.history.push({
        seq: ++this.seq,
        nodeId,
        op,
        result,
        invokedAt,
        respondedAt: ++this.clock,
      });
      return result;
    } finally {
      release();
    }
  }

  async transact(nodeId: string, ops: readonly Operation[]): Promise<OpResult[]> {
    const invokedAt = ++this.clock;
    const release = await this.mutex.acquire();
    try {
      const aborted = ops.some(
        (op) => op.kind === 'cas' && this.cell(op.key).version !== op.expectedVersion,
      );
      if (aborted) {
        const failed = ops.map((op) => {
          const c = this.cell(op.key);
          return { ok: false, conflict: true, version: c.version, value: c.value };
        });
        this.history.push({
          seq: ++this.seq,
          nodeId,
          op: { kind: 'read', key: `<tx:${ops.length} ops, aborted>` },
          result: failed[0],
          invokedAt,
          respondedAt: ++this.clock,
        });
        return failed;
      }
      const out = ops.map((op) => this.apply(op));
      this.history.push({
        seq: ++this.seq,
        nodeId,
        op: { kind: 'read', key: `<tx:${ops.length} ops, committed>` },
        result: out[out.length - 1] ?? { ok: true, conflict: false, version: 0n, value: undefined },
        invokedAt,
        respondedAt: ++this.clock,
      });
      return out;
    } finally {
      release();
    }
  }

  async update(nodeId: string, key: string, fn: (current: Value) => Value): Promise<OpResult> {
    const invokedAt = ++this.clock;
    const release = await this.mutex.acquire();
    try {
      const current = this.cell(key);
      const result = this.apply({ kind: 'write', key, value: fn(current.value) });
      this.history.push({
        seq: ++this.seq,
        nodeId,
        op: { kind: 'write', key, value: result.value },
        result,
        invokedAt,
        respondedAt: ++this.clock,
      });
      return result;
    } finally {
      release();
    }
  }

  async updateOptimistic(
    nodeId: string,
    key: string,
    fn: (current: Value) => Value,
    maxRetries = 256,
  ): Promise<OpResult> {
    for (let attempt = 0; attempt < maxRetries; attempt++) {
      const current = await this.execute(nodeId, { kind: 'read', key });
      const next = fn(current.value);
      const cas = await this.execute(nodeId, {
        kind: 'cas',
        key,
        expectedVersion: current.version,
        value: next,
      });
      if (cas.ok) return cas;
    }
    throw new Error(`update on key ${key} exceeded ${maxRetries} retries`);
  }

  async transactFn(nodeId: string, fn: (tx: TxView) => void): Promise<void> {
    const invokedAt = ++this.clock;
    const release = await this.mutex.acquire();
    try {
      const pending = new Map<string, Value>();
      const view: TxView = {
        get: (key) => {
          if (pending.has(key)) {
            const c = this.cell(key);
            return { version: c.version, value: pending.get(key) };
          }
          const c = this.cell(key);
          return { version: c.version, value: c.value };
        },
        set: (key, value) => {
          pending.set(key, value);
        },
      };
      fn(view);
      let last: OpResult = { ok: true, conflict: false, version: 0n, value: undefined };
      for (const [key, value] of pending) {
        last = this.apply({ kind: 'write', key, value });
      }
      this.history.push({
        seq: ++this.seq,
        nodeId,
        op: { kind: 'read', key: `<tx:fn ${pending.size} writes, committed>` },
        result: last,
        invokedAt,
        respondedAt: ++this.clock,
      });
    } finally {
      release();
    }
  }
}

export interface TxView {
  get(key: string): { version: bigint; value: Value };
  set(key: string, value: Value): void;
}
