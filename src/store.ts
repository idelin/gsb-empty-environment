export interface SetOp {
  readonly op: "set";
  readonly key: string;
  readonly value: unknown;
}

export type Op = SetOp;

export interface Transaction {
  set(key: string, value: unknown): void;
  get(key: string): unknown;
  has(key: string): boolean;
}

export interface Snapshot {
  readonly version: number;
  get(key: string): unknown;
  has(key: string): boolean;
  entries(): IterableIterator<[string, unknown]>;
}

export class Store {
  private committed = new Map<string, unknown>();
  private oplog: Op[] = [];
  private version = 0;

  get(key: string): unknown {
    return this.committed.get(key);
  }

  has(key: string): boolean {
    return this.committed.has(key);
  }

  get currentVersion(): number {
    return this.version;
  }

  get log(): readonly Op[] {
    return this.oplog;
  }

  snapshot(): Snapshot {
    const frozen = new Map(this.committed);
    const version = this.version;
    return {
      version,
      get: (key) => frozen.get(key),
      has: (key) => frozen.has(key),
      entries: () => frozen.entries(),
    };
  }

  transaction<T>(fn: (tx: Transaction) => T): T {
    const pending = new Map<string, unknown>();
    const ops: Op[] = [];
    const committed = this.committed;
    const tx: Transaction = {
      set(key, value) {
        pending.set(key, value);
        ops.push({ op: "set", key, value });
      },
      get(key) {
        return pending.has(key) ? pending.get(key) : committed.get(key);
      },
      has(key) {
        return pending.has(key) || committed.has(key);
      },
    };
    const result = fn(tx);
    for (const [key, value] of pending) committed.set(key, value);
    this.oplog.push(...ops);
    this.version++;
    return result;
  }

  static replay(ops: readonly Op[]): Store {
    const store = new Store();
    store.transaction((tx) => {
      for (const op of ops) tx.set(op.key, op.value);
    });
    return store;
  }
}
