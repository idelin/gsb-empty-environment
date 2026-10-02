import { join } from 'node:path';
import { InvalidStateError, UnknownTransactionError } from './errors.ts';
import { LockManager } from './locks.ts';
import { Store } from './store.ts';
import { Wal } from './wal.ts';

export type TxState = 'ACTIVE' | 'PREPARED' | 'COMMITTED' | 'ROLLEDBACK';

export interface TxInfo {
  xid: string;
  state: TxState;
  writes: Record<string, unknown>;
}

/**
 * XA-style local resource manager.
 *
 * Protocol (driven by an external transaction coordinator):
 *   begin -> write* -> prepare -> commit | rollback
 *
 * Durability contract: once prepare() returns, the PREPARED record and the
 * staged writes are fsync'ed to the WAL. After a crash, open() replays the
 * WAL, restores the in-doubt transaction and re-acquires its locks. The
 * locks and the prepare promise are held until the coordinator issues an
 * explicit commit() or rollback().
 */
export class ResourceManager {
  private readonly wal: Wal;
  private readonly store: Store;
  private readonly locks = new LockManager();
  private readonly txs = new Map<string, TxInfo>();
  readonly dir: string;

  private constructor(dir: string) {
    this.dir = dir;
    this.wal = new Wal(join(dir, 'wal.log'));
    this.store = new Store(join(dir, 'store.json'));
  }

  /** Opens (or creates) the RM directory and recovers state from the WAL. */
  static open(dir: string): ResourceManager {
    const rm = new ResourceManager(dir);
    rm.recover();
    return rm;
  }

  private recover(): void {
    for (const rec of Wal.readAll(this.wal.file)) {
      switch (rec.op) {
        case 'begin':
          this.txs.set(rec.xid, { xid: rec.xid, state: 'ACTIVE', writes: {} });
          break;
        case 'prepared': {
          const tx = this.txs.get(rec.xid) ?? { xid: rec.xid, state: 'ACTIVE' as TxState, writes: {} };
          tx.state = 'PREPARED';
          tx.writes = rec.writes;
          this.txs.set(rec.xid, tx);
          this.locks.acquire(rec.xid, Object.keys(rec.writes));
          break;
        }
        case 'committed':
        case 'rolledback': {
          const tx = this.txs.get(rec.xid);
          if (tx) {
            tx.state = rec.op === 'committed' ? 'COMMITTED' : 'ROLLEDBACK';
            this.locks.release(rec.xid);
          }
          break;
        }
      }
    }
  }

  begin(xid: string): void {
    const existing = this.txs.get(xid);
    if (existing && (existing.state === 'ACTIVE' || existing.state === 'PREPARED')) {
      throw new InvalidStateError(xid, `cannot begin, already ${existing.state}`);
    }
    this.wal.append({ op: 'begin', xid, ts: Date.now() });
    this.txs.set(xid, { xid, state: 'ACTIVE', writes: {} });
  }

  /** Stages a write and acquires an exclusive lock on the key (strict 2PL). */
  write(xid: string, key: string, value: unknown): void {
    const tx = this.requireTx(xid, 'ACTIVE');
    this.locks.acquire(xid, [key]);
    tx.writes[key] = value;
  }

  /** Reads staged writes of the own transaction first, then committed data. */
  read(xid: string, key: string): unknown {
    const tx = this.requireTx(xid);
    if (key in tx.writes) return tx.writes[key];
    const holder = this.locks.holderOf(key);
    if (holder !== undefined && holder !== xid) {
      // Reading uncommitted data of an in-doubt transaction is not allowed.
      throw new InvalidStateError(xid, `resource "${key}" is locked by in-doubt transaction "${holder}"`);
    }
    return this.store.get(key);
  }

  /**
   * Votes YES and makes the prepare durable: the staged writes are appended
   * to the WAL and fsync'ed before this method returns. Locks stay held.
   */
  prepare(xid: string): 'YES' {
    const tx = this.requireTx(xid, 'ACTIVE');
    this.wal.append({ op: 'prepared', xid, ts: Date.now(), writes: { ...tx.writes } });
    tx.state = 'PREPARED';
    return 'YES';
  }

  /** Applies the staged writes to the durable store and releases locks. */
  commit(xid: string): void {
    const tx = this.requireTx(xid, 'PREPARED');
    this.store.apply(tx.writes);
    this.wal.append({ op: 'committed', xid, ts: Date.now() });
    tx.state = 'COMMITTED';
    this.locks.release(xid);
  }

  /** Discards the staged writes and releases locks. */
  rollback(xid: string): void {
    const tx = this.requireTx(xid);
    if (tx.state !== 'ACTIVE' && tx.state !== 'PREPARED') {
      throw new InvalidStateError(xid, `cannot rollback in state ${tx.state}`);
    }
    this.wal.append({ op: 'rolledback', xid, ts: Date.now() });
    tx.state = 'ROLLEDBACK';
    tx.writes = {};
    this.locks.release(xid);
  }

  status(xid: string): TxState | 'UNKNOWN' {
    return this.txs.get(xid)?.state ?? 'UNKNOWN';
  }

  isLocked(key: string): boolean {
    return this.locks.isLocked(key);
  }

  lockHolder(key: string): string | undefined {
    return this.locks.holderOf(key);
  }

  /** Reads committed data without transactional context (for inspection). */
  committedValue(key: string): unknown {
    return this.store.get(key);
  }

  close(): void {
    this.wal.close();
  }

  private requireTx(xid: string, state?: TxState): TxInfo {
    const tx = this.txs.get(xid);
    if (!tx || tx.state === 'COMMITTED' || tx.state === 'ROLLEDBACK') {
      throw new UnknownTransactionError(xid);
    }
    if (state !== undefined && tx.state !== state) {
      throw new InvalidStateError(xid, `expected state ${state}, got ${tx.state}`);
    }
    return tx;
  }
}
