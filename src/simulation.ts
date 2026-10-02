import { PRNG, fnv1a } from './prng.ts';
import { LogManager } from './log.ts';
import { BufferPool, Disk } from './storage.ts';
import { TransactionManager } from './txn.ts';
import { RecoveryManager } from './recovery.ts';
import type { PageId, TxnId } from './types.ts';

type EventKind = 'gen' | 'checkpoint' | 'crash' | 'recover';

interface SimEvent {
  time: number;
  seq: number;
  kind: EventKind;
}

class EventQueue {
  private items: SimEvent[] = [];
  get size(): number {
    return this.items.length;
  }
  push(e: SimEvent): void {
    this.items.push(e);
    let i = this.items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      const a = this.items[p];
      const b = this.items[i];
      if (a.time < b.time || (a.time === b.time && a.seq < b.seq)) break;
      [this.items[p], this.items[i]] = [this.items[i], this.items[p]];
      i = p;
    }
  }
  pop(): SimEvent {
    const top = this.items[0];
    const last = this.items.pop()!;
    if (this.items.length > 0) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1;
        const r = l + 1;
        let m = i;
        if (l < this.items.length && this.less(this.items[l], this.items[m])) m = l;
        if (r < this.items.length && this.less(this.items[r], this.items[m])) m = r;
        if (m === i) break;
        [this.items[m], this.items[i]] = [this.items[i], this.items[m]];
        i = m;
      }
    }
    return top;
  }
  private less(a: SimEvent, b: SimEvent): boolean {
    return a.time < b.time || (a.time === b.time && a.seq < b.seq);
  }
}

export interface SimConfig {
  seed: number;
  steps: number;
  maxActiveTxns: number;
  numPages: number;
  checkpointEvery: number;
  crashAt: number[];
}

export interface SimStats {
  begun: number;
  committed: number;
  aborted: number;
  ops: number;
  checkpoints: number;
  crashes: number;
  recoveries: number;
  clrCount: number;
}

export interface SimResult {
  hash: string;
  expected: Map<PageId, number>;
  disk: Map<PageId, { value: number; pageLSN: number }>;
  mismatches: { pageId: PageId; expected: number; actual: number }[];
  stats: SimStats;
  logStableCount: number;
  openTxns: number;
  unflushedLog: number;
  dirtyFrames: number;
}

export function runSimulation(config: SimConfig): SimResult {
  const rng = new PRNG(config.seed);
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const tm = new TransactionManager(log, buffer);

  const stats: SimStats = {
    begun: 0, committed: 0, aborted: 0, ops: 0,
    checkpoints: 0, crashes: 0, recoveries: 0, clrCount: 0,
  };

  const expected = new Map<PageId, number>();
  const pending = new Map<TxnId, Map<PageId, number>>();
  const active: TxnId[] = [];
  const locks = new Map<PageId, TxnId>();
  const txnLocks = new Map<TxnId, PageId[]>();
  let crashed = false;

  const releaseLocks = (txn: TxnId) => {
    for (const p of txnLocks.get(txn) ?? []) locks.delete(p);
    txnLocks.delete(txn);
  };

  const queue = new EventQueue();
  let seq = 0;
  const schedule = (time: number, kind: EventKind) => queue.push({ time, seq: seq++, kind });

  schedule(0, 'gen');
  if (config.checkpointEvery > 0) schedule(config.checkpointEvery, 'checkpoint');
  for (const c of [...config.crashAt].sort((a, b) => a - b)) schedule(c, 'crash');

  const applyPending = (txn: TxnId) => {
    const m = pending.get(txn);
    if (!m) return;
    for (const [page, delta] of m) expected.set(page, (expected.get(page) ?? 0) + delta);
  };

  let steps = 0;
  let time = 0;
  while (steps < config.steps && queue.size > 0) {
    const ev = queue.pop();
    time = ev.time;
    steps++;
    switch (ev.kind) {
      case 'gen': {
        if (!crashed) {
          const r = rng.next();
          if (active.length === 0 || (active.length < config.maxActiveTxns && r < 0.3)) {
            const t = tm.begin();
            active.push(t);
            pending.set(t, new Map());
            stats.begun++;
          } else if (r < 0.85) {
            const txn = active[rng.int(active.length)];
            const page = rng.int(config.numPages);
            const owner = locks.get(page);
            if (owner === undefined || owner === txn) {
              locks.set(page, txn);
              let held = txnLocks.get(txn);
              if (!held) txnLocks.set(txn, (held = []));
              if (!held.includes(page)) held.push(page);
              const delta = rng.int(10) + 1;
              tm.update(txn, page, delta);
              const m = pending.get(txn)!;
              m.set(page, (m.get(page) ?? 0) + delta);
              stats.ops++;
            }
          } else {
            const idx = rng.int(active.length);
            const txn = active[idx];
            if (rng.next() < 0.8) {
              tm.commit(txn);
              applyPending(txn);
              stats.committed++;
            } else {
              tm.abort(txn);
              stats.aborted++;
            }
            releaseLocks(txn);
            active.splice(idx, 1);
            pending.delete(txn);
          }
        }
        schedule(time + 1, 'gen');
        break;
      }
      case 'checkpoint': {
        if (!crashed) {
          tm.checkpoint();
          stats.checkpoints++;
        }
        schedule(time + config.checkpointEvery, 'checkpoint');
        break;
      }
      case 'crash': {
        buffer.crash();
        log.crash();
        tm.discardCrashedState();
        crashed = true;
        stats.crashes++;
        active.length = 0;
        pending.clear();
        locks.clear();
        txnLocks.clear();
        schedule(time + 1, 'recover');
        break;
      }
      case 'recover': {
        const rm = new RecoveryManager(log, disk, buffer);
        const res = rm.recover();
        crashed = false;
        stats.recoveries++;
        stats.clrCount += res.undo.clrCount;
        break;
      }
    }
  }

  if (crashed) {
    const rm = new RecoveryManager(log, disk, buffer);
    const res = rm.recover();
    stats.recoveries++;
    stats.clrCount += res.undo.clrCount;
    crashed = false;
  }
  while (active.length > 0) {
    const txn = active.pop()!;
    tm.commit(txn);
    applyPending(txn);
    releaseLocks(txn);
    pending.delete(txn);
    stats.committed++;
  }
  tm.checkpoint();
  stats.checkpoints++;

  const diskSnap = disk.snapshot();
  const mismatches: SimResult['mismatches'] = [];
  const allPages = new Set<PageId>([...expected.keys(), ...diskSnap.keys()]);
  for (const p of allPages) {
    const exp = expected.get(p) ?? 0;
    const act = diskSnap.get(p)?.value ?? 0;
    if (exp !== act) mismatches.push({ pageId: p, expected: exp, actual: act });
  }

  const hashInput =
    [...expected.keys()].sort((a, b) => a - b).map((p) => `${p}:${expected.get(p)}`).join('|') +
    '#' + JSON.stringify(stats) +
    '#' + log.stableCount;

  return {
    hash: fnv1a(hashInput),
    expected,
    disk: diskSnap,
    mismatches,
    stats,
    logStableCount: log.stableCount,
    openTxns: tm.txnTable.size,
    unflushedLog: log.bufferedCount,
    dirtyFrames: buffer.dirtyCount,
  };
}
