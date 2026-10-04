import {
  ClientRequest,
  ClientResponse,
  Key,
  Mutation,
  StoredRecord,
} from './types';

export type TraceFn = (msg: string) => void;

/**
 * A shard server (A = source, B = target).
 *
 * - Every accepted write is assigned a monotonically increasing version.
 * - Deletes are stored as tombstones so stale replicated puts cannot
 *   resurrect them.
 * - Once fenced, new writes are rejected, but retries of previously
 *   accepted requests still return their original (cached) results.
 * - Replication is applied idempotently: an incoming mutation wins only
 *   if its version is strictly newer than the stored one.
 */
export class ShardServer {
  private store = new Map<Key, StoredRecord>();
  private requestCache = new Map<string, ClientResponse>();
  private seq = 0;
  readonly opLog: Mutation[] = [];
  fenced = false;
  active = false;
  highWater = 0; // contiguous replicated version applied (used on B)
  private appliedBuffer = new Set<number>(); // out-of-order versions waiting on gaps

  constructor(readonly name: string, private trace: TraceFn) {}

  get currentSeq(): number {
    return this.seq;
  }

  fence(): number {
    this.fenced = true;
    return this.seq;
  }

  activate(startSeq: number): void {
    this.active = true;
    this.seq = startSeq;
  }

  /** Client write path. */
  handleRequest(req: ClientRequest, t: number): ClientResponse {
    const cached = this.requestCache.get(req.requestId);
    if (cached) {
      this.trace(
        `[t=${t}] ${this.name}: retry ${req.requestId} -> returns OLD cached result (status=${cached.status}` +
          `${cached.version !== undefined ? `, v${cached.version}` : ''})`,
      );
      return cached;
    }
    if (this.fenced) {
      this.trace(
        `[t=${t}] ${this.name}: REJECT new write ${req.requestId} ${req.op} ${req.key} (fenced)`,
      );
      return { requestId: req.requestId, status: 'fenced' };
    }
    if (!this.active) {
      return { requestId: req.requestId, status: 'not-active' };
    }
    const version = ++this.seq;
    const value = req.op === 'put' ? req.value! : null;
    this.store.set(req.key, { value, version });
    this.opLog.push({ key: req.key, value, version, requestId: req.requestId });
    const res: ClientResponse = { requestId: req.requestId, status: 'ok', version };
    this.requestCache.set(req.requestId, res);
    this.trace(
      `[t=${t}] ${this.name}: accept ${req.requestId} ${req.op} ${req.key}` +
        `${req.op === 'put' ? '=' + req.value : ''} (v${version})`,
    );
    return res;
  }

  /**
   * Replication apply path (idempotent, order-independent).
   * Returns true if the mutation was applied, false if it was a
   * duplicate / stale redelivery.
   */
  applyReplication(m: Mutation, t: number, channel: 'base' | 'delta'): boolean {
    const cur = this.store.get(m.key);
    // The version's effect is reflected in the store even when the message
    // itself is stale (a newer version already supersedes it), so it always
    // counts towards the contiguous high-water mark.
    this.appliedBuffer.add(m.version);
    while (this.appliedBuffer.delete(this.highWater + 1)) {
      this.highWater++;
    }
    if (cur && cur.version >= m.version) {
      this.trace(
        `[t=${t}] ${this.name}: ignore stale ${channel} ${m.key}@v${m.version} (have v${cur.version})`,
      );
      return false;
    }
    this.store.set(m.key, { value: m.value, version: m.version });
    this.trace(
      `[t=${t}] ${this.name}: apply ${channel} ${m.key}@v${m.version} -> ` +
        `${m.value === null ? 'TOMBSTONE' : m.value}`,
    );
    return true;
  }

  read(key: Key): { found: boolean; value?: string; version: number } {
    const rec = this.store.get(key);
    if (!rec) return { found: false, version: 0 };
    if (rec.value === null) return { found: false, version: rec.version };
    return { found: true, value: rec.value, version: rec.version };
  }

  /** Keys present in the store, including tombstoned ones (for base copy). */
  storeKeys(): Key[] {
    return [...this.store.keys()];
  }

  /** Current record for a key, including tombstones (for base copy). */
  rawRecord(key: Key): StoredRecord | undefined {
    return this.store.get(key);
  }

  /** Read-only snapshot of the full store (for the checker). */
  inspect(): Map<Key, StoredRecord> {
    return new Map(this.store);
  }
}
