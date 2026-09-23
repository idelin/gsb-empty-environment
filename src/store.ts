/**
 * Versioned state store.
 *
 * - Schema upgrades run explicit, registered migrations (chain resolved BFS).
 * - Transactions commit atomically: readers never observe intermediate state.
 * - Subscriptions are paused during migration and flushed once with the
 *   fully migrated state; a failed migration restores old schema + state.
 * - Field renames declared by a migration rebind selector paths.
 * - Unknown migration chains are rejected before any state is touched.
 * - Every committed op / migration is appended to an append-only log that
 *   can be replayed from the initial snapshot to reconstruct the state.
 * - A separate audit log records every migration attempt (committed or
 *   rolled back) together with the version mapping used.
 */

export type Path = readonly string[];
export type State = Record<string, unknown>;

export interface StateOp {
  type: 'set' | 'delete';
  path: Path;
  value?: unknown;
}

export interface Migration {
  from: number;
  to: number;
  description?: string;
  /** Declarative "old.dot.path" -> "new.dot.path" renames; used to rebind selectors. */
  renames?: Record<string, string>;
  /** Explicit state transform. Throwing aborts the whole chain. */
  migrate(state: State): State;
}

export interface OpLogEntry {
  kind: 'op';
  schemaVersion: number;
  op: StateOp;
}

export interface MigrationLogEntry {
  kind: 'migration';
  from: number;
  to: number;
  renames: Record<string, string>;
}

export type LogEntry = OpLogEntry | MigrationLogEntry;

export interface AuditEntry {
  seq: number;
  at: string;
  requestedFrom: number;
  requestedTo: number;
  chain: number[];
  renames: Record<string, string>;
  status: 'committed' | 'rolled-back' | 'rejected';
  error?: string;
}

export class UnknownMigrationChainError extends Error {
  readonly from: number;
  readonly to: number;
  readonly knownVersions: number[];

  constructor(from: number, to: number, knownVersions: number[]) {
    super(
      `No migration chain from schema v${from} to v${to}. ` +
        `Known versions: [${knownVersions.join(', ')}]`,
    );
    this.name = 'UnknownMigrationChainError';
    this.from = from;
    this.to = to;
    this.knownVersions = knownVersions;
  }
}

export class MigrationConflictError extends Error {
  constructor(action: string) {
    super(`Cannot ${action} while a migration is in progress`);
    this.name = 'MigrationConflictError';
  }
}

/* ---------- small pure helpers ---------- */

export function parsePath(path: string): string[] {
  if (path.trim() === '') throw new Error('Empty path');
  return path.split('.');
}

function getAt(obj: unknown, path: Path): unknown {
  let cur = obj;
  for (const key of path) {
    if (cur === null || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
}

function setAt(obj: State, path: Path, value: unknown): void {
  let cur: Record<string, unknown> = obj;
  for (let i = 0; i < path.length - 1; i++) {
    const next = cur[path[i]];
    if (next === null || typeof next !== 'object' || Array.isArray(next)) {
      cur[path[i]] = {};
    }
    cur = cur[path[i]] as Record<string, unknown>;
  }
  cur[path[path.length - 1]] = value;
}

function deleteAt(obj: State, path: Path): void {
  let cur: Record<string, unknown> = obj;
  for (let i = 0; i < path.length - 1; i++) {
    const next = cur[path[i]];
    if (next === null || typeof next !== 'object') return;
    cur = next as Record<string, unknown>;
  }
  delete cur[path[path.length - 1]];
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (typeof a !== 'object') return false;
  const aArr = Array.isArray(a);
  const bArr = Array.isArray(b);
  if (aArr !== bArr) return false;
  if (aArr && bArr) {
    if (a.length !== (b as unknown[]).length) return false;
    return a.every((v, i) => deepEqual(v, (b as unknown[])[i]));
  }
  const aKeys = Object.keys(a as object);
  const bKeys = Object.keys(b as object);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every(
    (k) =>
      Object.prototype.hasOwnProperty.call(b, k) &&
      deepEqual((a as State)[k], (b as State)[k]),
  );
}

function applyOp(state: State, op: StateOp): void {
  if (op.path.length === 0) throw new Error('Op path must not be empty');
  if (op.type === 'set') setAt(state, op.path, structuredClone(op.value));
  else deleteAt(state, op.path);
}

function rebindPath(path: string, renames: Record<string, string>): string {
  for (const [from, to] of Object.entries(renames)) {
    if (path === from) return to;
    if (path.startsWith(from + '.')) return to + path.slice(from.length);
  }
  return path;
}

/* ---------- store ---------- */

export interface StoreOptions {
  schemaVersion: number;
  initialState: State;
  migrations?: Migration[];
}

interface Subscription {
  path: string;
  callback: (value: unknown, state: State) => void;
  lastValue: unknown;
}

export class Store {
  #state: State;
  #schemaVersion: number;
  readonly #initialState: State;
  readonly #initialVersion: number;
  #migrations = new Map<number, Migration[]>();
  #subscriptions = new Set<Subscription>();
  #log: LogEntry[] = [];
  #audit: AuditEntry[] = [];
  #auditSeq = 0;
  #migrating = false;
  #transacting = false;

  constructor(options: StoreOptions) {
    this.#initialState = structuredClone(options.initialState);
    this.#initialVersion = options.schemaVersion;
    this.#state = structuredClone(options.initialState);
    this.#schemaVersion = options.schemaVersion;
    for (const m of options.migrations ?? []) this.registerMigration(m);
  }

  get schemaVersion(): number {
    return this.#schemaVersion;
  }

  get migrating(): boolean {
    return this.#migrating;
  }

  /** Readers only ever see the last committed snapshot. */
  getState(): State {
    return this.#state;
  }

  select<T = unknown>(path: string): T | undefined {
    return getAt(this.#state, parsePath(path)) as T | undefined;
  }

  /** Append-only op/migration log: the replay source of truth. */
  getLog(): readonly LogEntry[] {
    return this.#log;
  }

  /** Auditable record of every migration attempt and the version mapping used. */
  getAuditLog(): readonly AuditEntry[] {
    return this.#audit;
  }

  /** Registered version edges, for auditing the known version mapping. */
  getVersionMap(): { from: number; to: number; description?: string }[] {
    const edges: { from: number; to: number; description?: string }[] = [];
    for (const list of this.#migrations.values()) {
      for (const m of list) {
        edges.push(
          m.description === undefined
            ? { from: m.from, to: m.to }
            : { from: m.from, to: m.to, description: m.description },
        );
      }
    }
    return edges.sort((a, b) => a.from - b.from || a.to - b.to);
  }

  registerMigration(migration: Migration): void {
    if (migration.to <= migration.from) {
      throw new Error(`Migration target v${migration.to} must be > v${migration.from}`);
    }
    const list = this.#migrations.get(migration.from) ?? [];
    if (list.some((m) => m.to === migration.to)) {
      throw new Error(`Duplicate migration v${migration.from} -> v${migration.to}`);
    }
    list.push(migration);
    this.#migrations.set(migration.from, list);
  }

  subscribe(
    path: string,
    callback: (value: unknown, state: State) => void,
  ): () => void {
    parsePath(path); // validate eagerly
    const sub: Subscription = {
      path,
      callback,
      lastValue: getAt(this.#state, parsePath(path)),
    };
    this.#subscriptions.add(sub);
    return () => this.#subscriptions.delete(sub);
  }

  /**
   * Apply ops inside an atomic transaction. `build` receives a read-only
   * snapshot of the pre-transaction state for computing ops; only the
   * returned ops are applied, so every committed change is logged and the
   * log stays a sufficient replay source. `getState()` keeps returning the
   * previous committed snapshot until commit, so readers never observe
   * intermediate state. Any throw discards everything and notifies nobody.
   */
  transact(build: (snapshot: Readonly<State>) => StateOp[]): void {
    if (this.#migrating) throw new MigrationConflictError('transact');
    if (this.#transacting) throw new Error('Nested transactions are not supported');
    this.#transacting = true;
    let ops: StateOp[];
    try {
      ops = build(structuredClone(this.#state));
    } finally {
      this.#transacting = false;
    }
    const draft = structuredClone(this.#state);
    for (const op of ops) applyOp(draft, op);
    this.#state = draft; // atomic commit
    for (const op of ops) {
      this.#log.push({ kind: 'op', schemaVersion: this.#schemaVersion, op });
    }
    this.#notify();
  }

  /** Convenience wrapper for a single set. */
  set(path: string, value: unknown): void {
    this.transact(() => [{ type: 'set', path: parsePath(path), value }]);
  }

  /**
   * Migrate to `targetVersion` by resolving a chain of registered migrations.
   * The whole chain runs against a private draft while subscriptions are
   * paused; on success the new state + schema are committed atomically and
   * subscribers are notified exactly once. On any failure the old schema and
   * state remain untouched and the attempt is audited as rolled back.
   */
  migrateTo(targetVersion: number): void {
    if (this.#migrating) throw new MigrationConflictError('migrate');
    const from = this.#schemaVersion;
    const chain = this.#resolveChain(from, targetVersion);
    if (chain === null) {
      this.#recordAudit(from, targetVersion, [], {}, 'rejected',
        `Unknown migration chain v${from} -> v${targetVersion}`);
      throw new UnknownMigrationChainError(from, targetVersion, this.#knownVersions());
    }
    if (chain.length === 0) return; // already at target

    const renames: Record<string, string> = {};
    for (const m of chain) Object.assign(renames, m.renames ?? {});

    this.#migrating = true; // pause subscriptions + block transactions
    let draft: State;
    try {
      draft = structuredClone(this.#state);
      for (const m of chain) draft = m.migrate(draft);
    } catch (err) {
      this.#migrating = false;
      this.#recordAudit(from, targetVersion, chain.map((m) => m.to), renames,
        'rolled-back', err instanceof Error ? err.message : String(err));
      throw err; // old schema + state fully intact
    }

    // Atomic commit of the fully migrated state.
    this.#state = draft;
    this.#schemaVersion = targetVersion;
    for (const m of chain) {
      this.#log.push({
        kind: 'migration',
        from: m.from,
        to: m.to,
        renames: m.renames ?? {},
      });
    }
    for (const sub of this.#subscriptions) {
      sub.path = rebindPath(sub.path, renames); // field renames rebind selectors
    }
    this.#recordAudit(from, targetVersion, chain.map((m) => m.to), renames, 'committed');
    this.#migrating = false;
    this.#notify(); // single flush with the complete new state
  }

  /**
   * Replay the append-only log from the initial snapshot and return the
   * reconstructed state. Proves the log is a sufficient source of truth.
   */
  replay(): State {
    let state = structuredClone(this.#initialState);
    for (const entry of this.#log) {
      if (entry.kind === 'op') {
        applyOp(state, entry.op);
      } else {
        const chain = this.#resolveChain(entry.from, entry.to);
        if (chain === null || chain.length !== 1) {
          throw new UnknownMigrationChainError(entry.from, entry.to, this.#knownVersions());
        }
        state = chain[0].migrate(state);
      }
    }
    return state;
  }

  /** True when replaying the log reproduces the current committed state. */
  verifyReplay(): boolean {
    return deepEqual(this.replay(), this.#state);
  }

  /* ---------- internals ---------- */

  #notify(): void {
    for (const sub of this.#subscriptions) {
      const value = getAt(this.#state, parsePath(sub.path));
      if (!deepEqual(value, sub.lastValue)) {
        sub.lastValue = structuredClone(value);
        sub.callback(value, this.#state);
      }
    }
  }

  #resolveChain(from: number, to: number): Migration[] | null {
    if (from === to) return [];
    // BFS over the version graph for the shortest explicit chain.
    const queue: { version: number; chain: Migration[] }[] = [{ version: from, chain: [] }];
    const visited = new Set<number>([from]);
    while (queue.length > 0) {
      const { version, chain } = queue.shift()!;
      for (const m of this.#migrations.get(version) ?? []) {
        if (visited.has(m.to)) continue;
        const next = [...chain, m];
        if (m.to === to) return next;
        visited.add(m.to);
        queue.push({ version: m.to, chain: next });
      }
    }
    return null;
  }

  #knownVersions(): number[] {
    const versions = new Set<number>([this.#initialVersion, this.#schemaVersion]);
    for (const [from, list] of this.#migrations) {
      versions.add(from);
      for (const m of list) versions.add(m.to);
    }
    return [...versions].sort((a, b) => a - b);
  }

  #recordAudit(
    requestedFrom: number,
    requestedTo: number,
    chain: number[],
    renames: Record<string, string>,
    status: AuditEntry['status'],
    error?: string,
  ): void {
    const entry: AuditEntry = {
      seq: ++this.#auditSeq,
      at: new Date().toISOString(),
      requestedFrom,
      requestedTo,
      chain: [requestedFrom, ...chain],
      renames,
      status,
    };
    if (error !== undefined) entry.error = error;
    this.#audit.push(entry);
  }
}
