import { GlobalReadViewAligner, SnapshotNotReadyError } from './aligner.ts';
import type { CdcEvent } from './types.ts';

const aligner = new GlobalReadViewAligner({
  shards: ['shard-1', 'shard-2'],
  accounts: {
    alice: { shard: 'shard-1', balance: 100 },
    bob: { shard: 'shard-2', balance: 100 },
  },
});

const INITIAL_TOTAL = 200;

function logStatus(label: string): void {
  const status = aligner.status();
  console.log(
    `[${label}] ready=${status.ready} safeTime=${status.safeTime} ` +
      `watermarks=${JSON.stringify(status.shardWatermarks)} openTxns=${JSON.stringify(status.openTxnIds)}`,
  );
}

function tryQuery(label: string): void {
  try {
    const snapshot = aligner.snapshot();
    console.log(
      `[${label}] SNAPSHOT @T=${snapshot.safeTime}: alice=${snapshot.balanceOf('alice')} ` +
        `bob=${snapshot.balanceOf('bob')} total=${snapshot.totalMoney()}`,
    );
    if (snapshot.totalMoney() !== INITIAL_TOTAL) {
      console.error('INVARIANT VIOLATED: money created or destroyed!');
      process.exit(1);
    }
  } catch (error) {
    if (error instanceof SnapshotNotReadyError) {
      console.log(`[${label}] BLOCKED: ${error.message}`);
    } else {
      throw error;
    }
  }
}

function feed(label: string, event: CdcEvent): void {
  const desc =
    event.kind === 'heartbeat'
      ? `heartbeat lsn=${event.lsn}`
      : `${event.kind} txn=${event.txnId} lsn=${event.lsn} ops=${JSON.stringify(event.ops)}`;
  console.log(`\n>>> ${event.shard}: ${desc}`);
  aligner.ingest(event);
  logStatus(label);
}

console.log('=== Global Read-View Aligner demo: cross-shard transfer alice -> bob (50) ===\n');
tryQuery('t0');

feed('t1', {
  kind: 'participant-commit',
  shard: 'shard-1',
  lsn: 10,
  txnId: 'transfer-1',
  participants: ['shard-1', 'shard-2'],
  ops: [{ account: 'alice', delta: -50 }],
});
tryQuery('t1-query');

feed('t2', { kind: 'heartbeat', shard: 'shard-1', lsn: 100 });
feed('t3', { kind: 'heartbeat', shard: 'shard-2', lsn: 15 });
tryQuery('t3-query');

const stale = aligner.snapshot({ allowStale: true });
console.log(
  `[t3-stale] read-only view @T=${stale.safeTime}: alice=${stale.balanceOf('alice')} ` +
    `bob=${stale.balanceOf('bob')} total=${stale.totalMoney()} (pre-transfer state, still consistent)`,
);

feed('t4', {
  kind: 'participant-commit',
  shard: 'shard-2',
  lsn: 20,
  txnId: 'transfer-1',
  participants: ['shard-1', 'shard-2'],
  ops: [{ account: 'bob', delta: 50 }],
});
feed('t5', { kind: 'heartbeat', shard: 'shard-2', lsn: 100 });
tryQuery('t4-query');

console.log('\n=== closure reached: consistent snapshot emitted, money conserved ===');
