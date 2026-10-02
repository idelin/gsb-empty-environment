import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  GlobalReadViewAligner,
  ProtocolViolationError,
  SnapshotNotReadyError,
} from '../src/aligner.ts';
import type { CdcEvent } from '../src/types.ts';

const SHARD_A = 'shard-1';
const SHARD_B = 'shard-2';

function makeAligner() {
  return new GlobalReadViewAligner({
    shards: [SHARD_A, SHARD_B],
    accounts: {
      alice: { shard: SHARD_A, balance: 100 },
      bob: { shard: SHARD_B, balance: 100 },
    },
  });
}

function transferLegs(txnId: string, amount: number, lsnA: number, lsnB: number): CdcEvent[] {
  return [
    {
      kind: 'participant-commit',
      shard: SHARD_A,
      lsn: lsnA,
      txnId,
      participants: [SHARD_A, SHARD_B],
      ops: [{ account: 'alice', delta: -amount }],
    },
    {
      kind: 'participant-commit',
      shard: SHARD_B,
      lsn: lsnB,
      txnId,
      participants: [SHARD_A, SHARD_B],
      ops: [{ account: 'bob', delta: amount }],
    },
  ];
}

describe('GlobalReadViewAligner', () => {
  it('blocks any query view before both shards report a watermark', () => {
    const aligner = makeAligner();
    assert.equal(aligner.isReady(), false);
    assert.throws(() => aligner.snapshot(), SnapshotNotReadyError);

    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 5 });
    assert.throws(() => aligner.snapshot(), SnapshotNotReadyError);
  });

  it('pins the safe time below an unclosed transfer and refuses external views', () => {
    const aligner = makeAligner();
    const [debit] = transferLegs('txn-1', 50, 10, 20);
    aligner.ingest(debit);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 100 });
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_B, lsn: 100 });

    const status = aligner.status();
    assert.equal(status.ready, false);
    assert.deepEqual(status.openTxnIds, ['txn-1']);
    assert.equal(status.safeTime, 9);
    assert.throws(() => aligner.snapshot(), /causal closure not reached/);
  });

  it('emits a consistent snapshot only after both transfer legs arrive', () => {
    const aligner = makeAligner();
    const [debit, credit] = transferLegs('txn-1', 50, 10, 20);
    aligner.ingest(debit);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 100 });
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_B, lsn: 15 });
    assert.throws(() => aligner.snapshot(), SnapshotNotReadyError);

    aligner.ingest(credit);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_B, lsn: 100 });
    const snapshot = aligner.snapshot();
    assert.equal(snapshot.balanceOf('alice'), 50);
    assert.equal(snapshot.balanceOf('bob'), 150);
    assert.equal(snapshot.totalMoney(), 200);
    assert.ok(snapshot.safeTime >= 20);
    assert.deepEqual(snapshot.includedTxnIds, ['txn-1']);
  });

  it('stale read-view never shows a half-applied transfer', () => {
    const aligner = makeAligner();
    const [debit, credit] = transferLegs('txn-1', 50, 10, 20);
    aligner.ingest(debit);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 100 });
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_B, lsn: 15 });

    const stale = aligner.snapshot({ allowStale: true });
    assert.equal(stale.safeTime, 9);
    assert.equal(stale.balanceOf('alice'), 100);
    assert.equal(stale.balanceOf('bob'), 100);
    assert.equal(stale.totalMoney(), 200);

    aligner.ingest(credit);
    const fresh = aligner.snapshot();
    assert.equal(fresh.totalMoney(), 200);
    assert.equal(fresh.balanceOf('alice'), 50);
    assert.equal(fresh.balanceOf('bob'), 150);
  });

  it('hides a closed cross-shard txn until the slowest shard watermark covers it', () => {
    const aligner = makeAligner();
    const [debit, credit] = transferLegs('txn-1', 30, 10, 20);
    aligner.ingest(debit);
    aligner.ingest(credit);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 15 });

    const early = aligner.snapshot();
    assert.equal(early.safeTime, 15);
    assert.equal(early.balanceOf('alice'), 100);
    assert.equal(early.balanceOf('bob'), 100);
    assert.equal(early.totalMoney(), 200);

    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 25 });
    const late = aligner.snapshot();
    assert.equal(late.balanceOf('alice'), 70);
    assert.equal(late.balanceOf('bob'), 130);
    assert.equal(late.totalMoney(), 200);
  });

  it('tracks several concurrent open transfers with the earliest pinning the safe time', () => {
    const aligner = makeAligner();
    const [debit1] = transferLegs('txn-1', 10, 10, 11);
    const [debit2] = transferLegs('txn-2', 20, 30, 31);
    aligner.ingest(debit1);
    aligner.ingest(debit2);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 200 });
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_B, lsn: 200 });

    assert.equal(aligner.status().safeTime, 9);
    assert.throws(() => aligner.snapshot(), SnapshotNotReadyError);
  });

  it('resolves waitForSnapshot only once causal closure is reached', async () => {
    const aligner = makeAligner();
    const [debit, credit] = transferLegs('txn-1', 50, 10, 20);
    aligner.ingest(debit);
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 100 });
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_B, lsn: 15 });

    let resolved = false;
    const pending = aligner.waitForSnapshot().then((snapshot) => {
      resolved = true;
      return snapshot;
    });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(resolved, false);

    aligner.ingest(credit);
    const snapshot = await pending;
    assert.equal(resolved, true);
    assert.equal(snapshot.totalMoney(), 200);
    assert.equal(snapshot.balanceOf('alice'), 50);
  });

  it('rejects out-of-order CDC events on the same shard', () => {
    const aligner = makeAligner();
    aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 10 });
    assert.throws(
      () => aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 10 }),
      ProtocolViolationError,
    );
    assert.throws(
      () => aligner.ingest({ kind: 'heartbeat', shard: SHARD_A, lsn: 9 }),
      ProtocolViolationError,
    );
  });

  it('rejects unknown shards, accounts and misplaced ops', () => {
    const aligner = makeAligner();
    assert.throws(
      () => aligner.ingest({ kind: 'heartbeat', shard: 'shard-99', lsn: 1 }),
      ProtocolViolationError,
    );
    assert.throws(
      () =>
        aligner.ingest({
          kind: 'local-commit',
          shard: SHARD_A,
          lsn: 1,
          txnId: 'txn-x',
          ops: [{ account: 'bob', delta: 1 }],
        }),
      /lives on shard/,
    );
  });

  it('keeps the money-conservation invariant under randomized interleavings', () => {
    for (let seed = 1; seed <= 50; seed += 1) {
      const random = mulberry32(seed);
      const aligner = makeAligner();
      const events: CdcEvent[] = [];
      for (let i = 0; i < 20; i += 1) {
        events.push(...transferLegs(`txn-${i}`, 1 + (i % 7), 10 + i * 2, 10 + i * 2));
      }
      events.push({ kind: 'heartbeat', shard: SHARD_A, lsn: 1000 });
      events.push({ kind: 'heartbeat', shard: SHARD_B, lsn: 1000 });

      const queueA = events.filter((e) => e.shard === SHARD_A);
      const queueB = events.filter((e) => e.shard === SHARD_B);
      while (queueA.length > 0 || queueB.length > 0) {
        const pickA = queueB.length === 0 || (queueA.length > 0 && random() < 0.5);
        const event = (pickA ? queueA : queueB).shift()!;
        aligner.ingest(event);
        if (aligner.isReady()) {
          assert.equal(aligner.snapshot().totalMoney(), 200, `seed ${seed}`);
        }
        if (aligner.status().safeTime !== null) {
          const stale = aligner.snapshot({ allowStale: true });
          assert.equal(stale.totalMoney(), 200, `stale seed ${seed}`);
        }
      }
      assert.equal(aligner.snapshot().totalMoney(), 200);
    }
  });
});

function mulberry32(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
