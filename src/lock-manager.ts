/**
 * Multi-node concurrency conflict arbiter: a centralized lock manager that
 * serializes every decision on the single-threaded event loop, so all state
 * transitions are atomic and the grant order forms a single linearizable
 * history. Deadlock freedom is guaranteed by the wound-wait scheme: an
 * older transaction requesting a lock held by a younger one "wounds"
 * (aborts) it, while a younger requester simply waits for the older one.
 * Wait-for edges therefore always point from younger to older transactions
 * and the wait-for graph is acyclic by construction.
 */

export type LockMode = 'S' | 'X';

export class LockAbortedError extends Error {
  readonly txnId: number;
  constructor(txnId: number) {
    super(`transaction ${txnId} was chosen as a wound-wait victim`);
    this.name = 'LockAbortedError';
    this.txnId = txnId;
  }
}

interface QueueEntry {
  txnId: number;
  mode: LockMode;
  isUpgrade: boolean;
  resolve: () => void;
  reject: (err: Error) => void;
  settled: boolean;
}

interface LockEntry {
  holders: Map<number, LockMode>;
  queue: QueueEntry[];
}

export class LockManager {
  private readonly table = new Map<string, LockEntry>();
  private readonly heldBy = new Map<number, Set<string>>();
  private readonly waitFor = new Map<number, Set<number>>();
  private readonly timestamps = new Map<number, number>();
  private clock = 0;
  private victimHandler: (txnId: number) => void = () => {};
  private readonly recordEvents: boolean;
  private readonly events: string[] = [];

  constructor(opts?: { recordEvents?: boolean }) {
    this.recordEvents = opts?.recordEvents ?? false;
  }

  /** Called synchronously when a transaction is wounded. The handler is
   *  expected to abort the victim (which releases its locks). */
  setVictimHandler(handler: (txnId: number) => void): void {
    this.victimHandler = handler;
  }

  registerTxn(txnId: number): void {
    if (this.timestamps.has(txnId)) {
      throw new Error(`LockManager: txn ${txnId} already registered`);
    }
    this.timestamps.set(txnId, ++this.clock);
    this.heldBy.set(txnId, new Set());
  }

  unregisterTxn(txnId: number): void {
    this.timestamps.delete(txnId);
    this.heldBy.delete(txnId);
    this.waitFor.delete(txnId);
    for (const edges of this.waitFor.values()) edges.delete(txnId);
  }

  /**
   * Acquire a lock. The grant/wait/wound decision is made synchronously,
   * which is what makes concurrent requests from many executor nodes
   * linearizable: the observable order is exactly the call order.
   */
  acquire(txnId: number, key: string, mode: LockMode): Promise<void> {
    const ts = this.timestamps.get(txnId);
    if (ts === undefined) {
      return Promise.reject(
        new Error(`LockManager: unregistered txn ${txnId}`),
      );
    }
    let entry = this.getOrCreate(key);

    const heldMode = entry.holders.get(txnId);
    if (heldMode === 'X' || heldMode === mode) return Promise.resolve();
    const isUpgrade = heldMode === 'S' && mode === 'X';

    // Wound-wait: an older requester wounds younger conflicting holders.
    // Wounding a victim synchronously aborts it, which releases its locks
    // and may wake queued requests — possibly granting the lock to *new*
    // younger holders. Repeat until no unwounded younger holder remains,
    // so an older transaction never ends up waiting on a younger one.
    const wounded = new Set<number>();
    for (;;) {
      entry = this.getOrCreate(key);
      let woundedAny = false;
      for (const [holder] of this.conflicts(entry, txnId, mode)) {
        if (wounded.has(holder)) continue;
        const holderTs = this.timestamps.get(holder);
        if (holderTs !== undefined && holderTs > ts) {
          wounded.add(holder);
          this.wound(holder);
          woundedAny = true;
        }
      }
      if (!woundedAny) break;
    }
    // Wounding may have synchronously aborted victims and released locks;
    // re-fetch the (possibly recreated) table entry.
    entry = this.getOrCreate(key);

    if (this.canGrant(entry, txnId, mode) && (isUpgrade || entry.queue.length === 0)) {
      this.grant(entry, key, txnId, mode);
      return Promise.resolve();
    }

    this.logEvent(`wait t${txnId} ${mode} ${key}`);
    for (const [holder] of this.conflicts(entry, txnId, mode)) {
      this.addWaitEdge(txnId, holder);
    }
    return new Promise<void>((resolve, reject) => {
      const qe: QueueEntry = {
        txnId,
        mode,
        isUpgrade,
        resolve,
        reject,
        settled: false,
      };
      if (isUpgrade) entry.queue.unshift(qe);
      else entry.queue.push(qe);
    });
  }

  release(txnId: number, key: string): void {
    const entry = this.table.get(key);
    if (!entry || !entry.holders.has(txnId)) {
      throw new Error(`LockManager: txn ${txnId} does not hold ${key}`);
    }
    entry.holders.delete(txnId);
    this.heldBy.get(txnId)?.delete(key);
    this.logEvent(`release t${txnId} ${key}`);
    this.processQueue(key, entry);
    if (entry.holders.size === 0 && entry.queue.length === 0) {
      this.table.delete(key);
    }
  }

  /** Release every lock held or awaited by a transaction (commit/abort). */
  releaseAll(txnId: number): void {
    this.dropQueuedRequests(txnId, new LockAbortedError(txnId));
    const keys = [...(this.heldBy.get(txnId) ?? [])];
    for (const key of keys) this.release(txnId, key);
    this.waitFor.delete(txnId);
    for (const edges of this.waitFor.values()) edges.delete(txnId);
  }

  /** Current wait-for graph as [younger, older] edge pairs. */
  waitForEdges(): Array<[number, number]> {
    const edges: Array<[number, number]> = [];
    for (const [from, tos] of this.waitFor) {
      for (const to of tos) edges.push([from, to]);
    }
    return edges;
  }

  /** Safety-net cycle check; wound-wait keeps this permanently false. */
  hasCycle(): boolean {
    const visiting = new Set<number>();
    const done = new Set<number>();
    const visit = (node: number): boolean => {
      if (done.has(node)) return false;
      if (visiting.has(node)) return true;
      visiting.add(node);
      for (const next of this.waitFor.get(node) ?? []) {
        if (visit(next)) return true;
      }
      visiting.delete(node);
      done.add(node);
      return false;
    };
    for (const node of this.waitFor.keys()) {
      if (visit(node)) return true;
    }
    return false;
  }

  /** True when no locks are held and no request is waiting (no leaks). */
  isQuiescent(): boolean {
    if (this.table.size !== 0) return false;
    for (const edges of this.waitFor.values()) {
      if (edges.size !== 0) return false;
    }
    return this.waitFor.size === 0;
  }

  /** Diagnostic dump used by leak assertions. */
  diagnostics(): string {
    const parts: string[] = [];
    for (const [key, entry] of this.table) {
      parts.push(
        `${key}: holders=[${[...entry.holders.entries()]
          .map(([t, m]) => `t${t}:${m}`)
          .join(',')}] queue=[${entry.queue
          .map((q) => `t${q.txnId}:${q.mode}`)
          .join(',')}]`,
      );
    }
    for (const [from, tos] of this.waitFor) {
      if (tos.size > 0) parts.push(`waitFor t${from} -> {${[...tos].join(',')}}`);
    }
    return parts.join('\n');
  }

  getEvents(): readonly string[] {
    return this.events;
  }

  private getOrCreate(key: string): LockEntry {
    let entry = this.table.get(key);
    if (!entry) {
      entry = { holders: new Map(), queue: [] };
      this.table.set(key, entry);
    }
    return entry;
  }

  private conflicts(
    entry: LockEntry,
    txnId: number,
    mode: LockMode,
  ): Array<[number, LockMode]> {
    const out: Array<[number, LockMode]> = [];
    for (const [holder, holderMode] of entry.holders) {
      if (holder === txnId) continue;
      if (mode === 'X' || holderMode === 'X') out.push([holder, holderMode]);
    }
    return out;
  }

  private canGrant(entry: LockEntry, txnId: number, mode: LockMode): boolean {
    return this.conflicts(entry, txnId, mode).length === 0;
  }

  private grant(entry: LockEntry, key: string, txnId: number, mode: LockMode): void {
    entry.holders.set(txnId, mode);
    this.heldBy.get(txnId)?.add(key);
    this.waitFor.delete(txnId);
    this.logEvent(`grant t${txnId} ${mode} ${key}`);
  }

  private processQueue(key: string, entry: LockEntry): void {
    for (;;) {
      const first = entry.queue[0];
      if (!first) return;
      if (first.mode === 'X') {
        if (!this.canGrant(entry, first.txnId, 'X')) return;
        entry.queue.shift();
        first.settled = true;
        this.grant(entry, key, first.txnId, 'X');
        first.resolve();
        return; // X is exclusive; nothing behind it can proceed yet.
      }
      // Grant the whole leading run of compatible shared requests.
      let grantedAny = false;
      while (entry.queue.length > 0 && entry.queue[0].mode === 'S') {
        const qe = entry.queue[0];
        if (!this.canGrant(entry, qe.txnId, 'S')) break;
        entry.queue.shift();
        qe.settled = true;
        this.grant(entry, key, qe.txnId, 'S');
        qe.resolve();
        grantedAny = true;
      }
      if (!grantedAny) return;
    }
  }

  private wound(victim: number): void {
    this.logEvent(`wound t${victim}`);
    this.dropQueuedRequests(victim, new LockAbortedError(victim));
    this.victimHandler(victim);
  }

  private dropQueuedRequests(txnId: number, err: Error): void {
    for (const [key, entry] of this.table) {
      for (let i = entry.queue.length - 1; i >= 0; i--) {
        const qe = entry.queue[i];
        if (qe.txnId === txnId && !qe.settled) {
          qe.settled = true;
          entry.queue.splice(i, 1);
          qe.reject(err);
        }
      }
      if (entry.holders.size === 0 && entry.queue.length === 0) {
        this.table.delete(key);
      }
    }
    this.waitFor.delete(txnId);
    for (const edges of this.waitFor.values()) edges.delete(txnId);
  }

  private addWaitEdge(from: number, to: number): void {
    let edges = this.waitFor.get(from);
    if (!edges) {
      edges = new Set();
      this.waitFor.set(from, edges);
    }
    edges.add(to);
  }

  private logEvent(event: string): void {
    if (this.recordEvents) this.events.push(event);
  }
}
