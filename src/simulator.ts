import { Prng } from './prng.ts';
import { Fnv1a64, sha256, stableStringify } from './hashutil.ts';
import { LogManager, MemoryWALStorage } from './wal.ts';
import type { WALStorage } from './wal.ts';
import { BufferPool, Disk } from './storage.ts';
import { TransactionManager } from './txn.ts';
import { LockConflictError, LockManager } from './lock.ts';
import { RecoveryManager } from './recovery.ts';
import type { RecoveryReport } from './recovery.ts';
import { verifyRecoveredState } from './verify.ts';
import type { VerificationResult } from './verify.ts';
import type { TxnId } from './types.ts';

type SimPayload =
  | { kind: 'begin' }
  | { kind: 'op'; txnId: TxnId }
  | { kind: 'commit'; txnId: TxnId }
  | { kind: 'abort'; txnId: TxnId }
  | { kind: 'checkpoint' }
  | { kind: 'steal' };

interface SimEvent {
  time: number;
  seq: number;
  payload: SimPayload;
}

// Deterministic discrete-time event queue (binary min-heap on (time, seq)).
export class EventQueue {
  private heap: SimEvent[] = [];
  private counter = 0;

  schedule(time: number, payload: SimPayload): void {
    const event: SimEvent = { time, seq: this.counter++, payload };
    const heap = this.heap;
    heap.push(event);
    let i = heap.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      const p = heap[parent];
      if (p.time < event.time || (p.time === event.time && p.seq < event.seq)) break;
      heap[i] = p;
      i = parent;
    }
    heap[i] = event;
  }

  pop(): SimEvent | undefined {
    const heap = this.heap;
    if (heap.length === 0) return undefined;
    const top = heap[0];
    const last = heap.pop() as SimEvent;
    if (heap.length > 0) {
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (left < heap.length && this.less(heap[left], heap[smallest])) smallest = left;
        if (right < heap.length && this.less(heap[right], heap[smallest])) smallest = right;
        if (smallest === i) break;
        heap[i] = heap[smallest];
        i = smallest;
      }
      heap[i] = last;
    }
    return top;
  }

  private less(a: SimEvent, b: SimEvent): boolean {
    return a.time < b.time || (a.time === b.time && a.seq < b.seq);
  }

  get size(): number {
    return this.heap.length;
  }

  clear(): void {
    this.heap = [];
  }
}

export interface SimConfig {
  seed: number;
  numPages?: number;
  keysPerPage?: number;
  maxActiveTxns?: number;
  bufferCapacity?: number;
  checkpointEvery?: number;
  crashAt?: number; // event index at which to inject the crash
  storage?: WALStorage;
}

export interface SimResult {
  seed: number;
  events: number;
  crashedAt: number;
  walLength: number;
  walHash: string;
  diskHash: string;
  committedTxns: number;
  abortedTxns: number;
  losers: TxnId[];
  winners: TxnId[];
  recovery: RecoveryReport;
  verification: VerificationResult;
  leaks: string[];
}

// Discrete-time event-driven deterministic database simulator with crash
// injection and ARIES recovery. Same seed => identical WAL, identical crash,
// identical recovery: bit-for-bit reproducible fault reproduction.
export class Simulator {
  private config: Required<Omit<SimConfig, 'storage' | 'crashAt'>> & { crashAt: number | null };
  private storage: WALStorage;

  constructor(config: SimConfig) {
    this.config = {
      seed: config.seed,
      numPages: config.numPages ?? 64,
      keysPerPage: config.keysPerPage ?? 8,
      maxActiveTxns: config.maxActiveTxns ?? 8,
      bufferCapacity: config.bufferCapacity ?? 16,
      checkpointEvery: config.checkpointEvery ?? 500,
      crashAt: config.crashAt ?? null,
    };
    this.storage = config.storage ?? new MemoryWALStorage();
  }

  run(maxEvents: number): SimResult {
    const cfg = this.config;
    const rand = new Prng(cfg.seed);
    const crashAt = cfg.crashAt ?? maxEvents;
    const disk = new Disk();
    const log = new LogManager(this.storage);
    const bp = new BufferPool(disk, cfg.bufferCapacity, log);
    const locks = new LockManager();
    const tm = new TransactionManager(log, bp, locks);
    const queue = new EventQueue();

    const active = new Set<TxnId>();
    let committed = 0;
    let aborted = 0;
    let time = 0;

    queue.schedule(0, { kind: 'begin' });
    queue.schedule(cfg.checkpointEvery, { kind: 'checkpoint' });
    queue.schedule(rand.int(3, 7), { kind: 'steal' });

    let events = 0;
    while (events < crashAt) {
      const event = queue.pop();
      if (!event) break;
      time = event.time;
      events++;
      const p = event.payload;
      switch (p.kind) {
        case 'begin': {
          if (active.size < cfg.maxActiveTxns) {
            const txnId = tm.begin();
            active.add(txnId);
            queue.schedule(time + rand.int(1, 3), { kind: 'op', txnId });
          }
          queue.schedule(time + rand.int(1, 4), { kind: 'begin' });
          break;
        }
        case 'op': {
          if (!active.has(p.txnId)) break;
          const pageId = rand.int(0, cfg.numPages - 1);
          const key = 'k' + rand.int(0, cfg.keysPerPage - 1);
          try {
            tm.update(p.txnId, pageId, key, rand.int(1, 1_000_000_000));
          } catch (err) {
            if (err instanceof LockConflictError) {
              // Immediate-abort policy on conflict: no waits, no deadlocks.
              tm.abort(p.txnId);
              active.delete(p.txnId);
              aborted++;
              break;
            }
            throw err;
          }
          const roll = rand.next();
          if (roll < 0.65) {
            queue.schedule(time + rand.int(1, 3), { kind: 'op', txnId: p.txnId });
          } else if (roll < 0.9) {
            queue.schedule(time + rand.int(1, 3), { kind: 'commit', txnId: p.txnId });
          } else {
            queue.schedule(time + rand.int(1, 3), { kind: 'abort', txnId: p.txnId });
          }
          break;
        }
        case 'commit': {
          if (!active.has(p.txnId)) break;
          tm.commit(p.txnId);
          active.delete(p.txnId);
          committed++;
          break;
        }
        case 'abort': {
          if (!active.has(p.txnId)) break;
          tm.abort(p.txnId);
          active.delete(p.txnId);
          aborted++;
          break;
        }
        case 'checkpoint': {
          tm.checkpoint();
          queue.schedule(time + cfg.checkpointEvery, { kind: 'checkpoint' });
          break;
        }
        case 'steal': {
          bp.flushRandomDirty(rand);
          queue.schedule(time + rand.int(3, 7), { kind: 'steal' });
          break;
        }
      }
    }

    // === Crash: volatile log tail and buffer pool are lost. ===
    log.crash();
    bp.discardAll();
    queue.clear();

    // === Recovery on a fresh log manager / buffer pool over the same disk. ===
    const recoveryLog = new LogManager(this.storage);
    const recoveryBp = new BufferPool(disk, cfg.bufferCapacity, recoveryLog);
    const recovery = new RecoveryManager(recoveryLog, recoveryBp).recover();

    const wal = recoveryLog.durable();
    const verification = verifyRecoveredState(disk, wal);

    const leaks: string[] = [];
    if (!recoveryBp.allClean()) leaks.push('buffer pool has dirty pages after recovery');
    if (recoveryBp.size !== 0 && !recoveryBp.allClean()) leaks.push('buffer pool not drained');
    if (recoveryLog.pendingTail !== 0) leaks.push('log tail not flushed after recovery');
    if (queue.size !== 0) leaks.push('event queue not drained');
    leaks.push(...verification.errors);

    const walHashFn = new Fnv1a64();
    for (const record of wal) walHashFn.update(stableStringify(record));
    const diskHash = sha256(
      stableStringify(disk.pageIds().map((id) => [id, disk.readPage(id)])),
    );

    return {
      seed: cfg.seed,
      events,
      crashedAt: crashAt,
      walLength: wal.length,
      walHash: walHashFn.digest(),
      diskHash,
      committedTxns: committed,
      abortedTxns: aborted,
      losers: recovery.losers,
      winners: recovery.winners,
      recovery,
      verification,
      leaks,
    };
  }
}

// Fault reproduction entry point: replaying (seed, maxEvents, crashAt) must
// reproduce the exact same WAL and post-recovery disk state.
export function reproduce(seed: number, maxEvents: number, crashAt?: number): SimResult {
  return new Simulator({ seed, crashAt }).run(maxEvents);
}
