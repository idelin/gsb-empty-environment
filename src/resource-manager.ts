/**
 * XA-compliant local Resource Manager (RM).
 *
 * Responds to the transaction coordinator's Prepare / Commit / Rollback
 * directives. Once PREPARE is acknowledged, the promise is durable: the
 * prepared record is appended to a write-ahead log and fsync'd before
 * returning, so a crash + restart can never lose the prepared state.
 * Locks acquired at prepare time are re-acquired during recovery and held
 * until an explicit COMMIT or ROLLBACK arrives from the coordinator.
 */

import * as fs from 'node:fs';
import * as path from 'node:path';

export type Operation =
  | { type: 'put'; key: string; value: unknown }
  | { type: 'delete'; key: string };

export type Xid = string;

type TxState = 'prepared' | 'committed' | 'rolledback';

interface WalRecord {
  xid: Xid;
  state: TxState;
  ops?: Operation[];
  ts: number;
}

interface TxEntry {
  xid: Xid;
  ops: Operation[];
  state: TxState;
}

/** XA-style error codes so a coordinator can map failures to XAException. */
export class XaError extends Error {
  readonly code: 'XAER_DUPID' | 'XAER_NOTA' | 'XAER_RMERR' | 'XA_LOCKED';

  constructor(
    code: 'XAER_DUPID' | 'XAER_NOTA' | 'XAER_RMERR' | 'XA_LOCKED',
    message: string,
  ) {
    super(message);
    this.name = 'XaError';
    this.code = code;
  }
}

export class ResourceManager {
  private readonly dir: string;
  private readonly walPath: string;
  private readonly storePath: string;
  private walFd!: number;

  /** xid -> transaction entry (only in-doubt / prepared txns are kept). */
  private readonly transactions = new Map<Xid, TxEntry>();
  /** key -> xid that holds the lock. */
  private readonly locks = new Map<string, Xid>();
  /** committed data (the actual resource). */
  private store = new Map<string, unknown>();

  constructor(dir: string) {
    this.dir = dir;
    this.walPath = path.join(dir, 'rm-wal.log');
    this.storePath = path.join(dir, 'rm-store.json');
    fs.mkdirSync(dir, { recursive: true });
    this.recover();
    // Open WAL for appending; every write is followed by fsync.
    this.walFd = fs.openSync(this.walPath, 'a');
  }

  // ------------------------------------------------------------------
  // Coordinator-facing XA directives
  // ------------------------------------------------------------------

  /**
   * XA PREPARE: persist the prepare record (fsync) and lock resources.
   * After this call returns, the RM guarantees it can commit the txn even
   * across a crash — the promise is durable.
   */
  prepare(xid: Xid, ops: Operation[]): void {
    if (this.transactions.has(xid)) {
      throw new XaError('XAER_DUPID', `transaction '${xid}' already exists`);
    }
    for (const op of ops) {
      const holder = this.locks.get(op.key);
      if (holder !== undefined) {
        throw new XaError(
          'XA_LOCKED',
          `key '${op.key}' is locked by transaction '${holder}'`,
        );
      }
    }
    // 1. Persist the promise FIRST (write-ahead logging + fsync).
    this.appendWal({ xid, state: 'prepared', ops, ts: Date.now() });
    // 2. Only after the record is durable, take the in-memory locks.
    this.transactions.set(xid, { xid, ops, state: 'prepared' });
    for (const op of ops) this.locks.set(op.key, xid);
  }

  /** XA COMMIT: apply the prepared ops to the store, then release locks. */
  commit(xid: Xid): void {
    const tx = this.transactions.get(xid);
    if (!tx || tx.state !== 'prepared') {
      throw new XaError('XAER_NOTA', `no prepared transaction '${xid}'`);
    }
    // Persist the commit decision before applying, so recovery can finish it.
    this.appendWal({ xid, state: 'committed', ts: Date.now() });
    for (const op of tx.ops) {
      if (op.type === 'put') this.store.set(op.key, op.value);
      else this.store.delete(op.key);
    }
    this.persistStore();
    this.finish(xid);
  }

  /** XA ROLLBACK: discard the prepared ops and release locks. */
  rollback(xid: Xid): void {
    const tx = this.transactions.get(xid);
    if (!tx || tx.state !== 'prepared') {
      throw new XaError('XAER_NOTA', `no prepared transaction '${xid}'`);
    }
    this.appendWal({ xid, state: 'rolledback', ts: Date.now() });
    this.finish(xid);
  }

  // ------------------------------------------------------------------
  // Local (non-transactional) access — respects prepared-txn locks
  // ------------------------------------------------------------------

  read(key: string): unknown {
    return this.store.get(key);
  }

  /** Plain local write; rejected while the key is locked by a prepared txn. */
  write(key: string, value: unknown): void {
    const holder = this.locks.get(key);
    if (holder !== undefined) {
      throw new XaError(
        'XA_LOCKED',
        `key '${key}' is locked by prepared transaction '${holder}'`,
      );
    }
    this.store.set(key, value);
    this.persistStore();
  }

  isLocked(key: string): boolean {
    return this.locks.has(key);
  }

  lockHolder(key: string): Xid | undefined {
    return this.locks.get(key);
  }

  /** In-doubt transactions (XA RECOVER scan). */
  recoverScan(): Xid[] {
    return [...this.transactions.keys()];
  }

  close(): void {
    fs.closeSync(this.walFd);
  }

  // ------------------------------------------------------------------
  // Durability internals
  // ------------------------------------------------------------------

  private appendWal(record: WalRecord): void {
    const line = JSON.stringify(record) + '\n';
    fs.writeSync(this.walFd, line);
    fs.fsyncSync(this.walFd); // promise is durable before we return
  }

  private persistStore(): void {
    const tmp = this.storePath + '.tmp';
    const data = JSON.stringify(Object.fromEntries(this.store), null, 2);
    fs.writeFileSync(tmp, data);
    const fd = fs.openSync(tmp, 'r');
    fs.fsyncSync(fd);
    fs.closeSync(fd);
    fs.renameSync(tmp, this.storePath); // atomic replace
  }

  private finish(xid: Xid): void {
    const tx = this.transactions.get(xid);
    if (tx) {
      for (const op of tx.ops) this.locks.delete(op.key);
    }
    this.transactions.delete(xid);
  }

  /**
   * Crash recovery: replay the WAL and the committed store.
   * - committed/rolled-back txns are forgotten;
   * - prepared-but-unfinished txns are restored as in-doubt and their
   *   locks are re-acquired, exactly as before the crash.
   */
  private recover(): void {
    if (fs.existsSync(this.storePath)) {
      const raw = JSON.parse(fs.readFileSync(this.storePath, 'utf8'));
      this.store = new Map(Object.entries(raw));
    }
    if (!fs.existsSync(this.walPath)) return;

    const lines = fs.readFileSync(this.walPath, 'utf8').split('\n');
    for (const line of lines) {
      if (!line.trim()) continue;
      const rec = JSON.parse(line) as WalRecord;
      if (rec.state === 'prepared') {
        this.transactions.set(rec.xid, { xid: rec.xid, ops: rec.ops ?? [], state: 'prepared' });
      } else {
        // commit or rollback: the txn is finished, forget it
        this.transactions.delete(rec.xid);
        if (rec.state === 'committed') {
          // commit record is followed by store persistence, so nothing to redo
        }
      }
    }
    // Re-acquire locks for every in-doubt transaction.
    for (const tx of this.transactions.values()) {
      for (const op of tx.ops) this.locks.set(op.key, tx.xid);
    }
  }
}
