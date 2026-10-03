/**
 * End-to-end demo:
 *   1. lock-free MPMC queue under 4 worker threads (CAS only, no locks)
 *   2. 5-node Multi-Paxos cluster: election -> lease fast path -> network
 *      partition -> old leader deposed -> hole repair -> dirty minority log
 *      safely overwritten -> convergence after heal.
 */
import { Worker } from 'node:worker_threads';
import { LockFreeQueue } from '../src/lockfree-queue.ts';
import { Cluster } from '../src/paxos/cluster.ts';
import { NOOP } from '../src/paxos/types.ts';

function show(value: unknown): string {
  return value === NOOP ? '<noop>' : String(value);
}

async function queueDemo(): Promise<void> {
  console.log('=== Lock-free MPMC queue (Vyukov, CAS-only, SharedArrayBuffer) ===');
  const workers = 4;
  const perWorker = 25_000;
  const total = workers * perWorker;
  const queue = new LockFreeQueue(1024);
  const bitmapBuffer = new SharedArrayBuffer(total * 4);
  const counterBuffer = new SharedArrayBuffer(4);
  const bitmap = new Int32Array(bitmapBuffer);
  const counter = new Int32Array(counterBuffer);

  const started = performance.now();
  await Promise.all(
    Array.from({ length: workers }, (_, workerId) =>
      new Promise((resolve, reject) => {
        const w = new Worker(new URL('../test/helpers/queue-worker.ts', import.meta.url), {
          workerData: { queueBuffer: queue.buffer, capacity: 1024, bitmapBuffer, counterBuffer, workerId, workers, perWorker },
        });
        w.once('message', resolve);
        w.once('error', reject);
      }),
    ),
  );
  const elapsed = performance.now() - started;
  const exactlyOnce = bitmap.every((n) => n === 1);
  console.log(`  ${workers} workers pushed/popped ${total.toLocaleString()} items in ${elapsed.toFixed(0)}ms`);
  console.log(`  throughput ~${Math.round((total / elapsed) * 1000).toLocaleString()} ops/s, every item consumed exactly once: ${exactlyOnce}`);
  console.log(`  queue drained, no leaks: ${queue.isEmpty() && Atomics.load(counter, 0) === total}\n`);
}

function paxosDemo(): void {
  console.log('=== Multi-Paxos: partition, deposed leader, hole repair ===');
  const c = new Cluster(5);
  const [n0, n1, n2] = c.nodes;

  console.log('[1] node0 campaigns and commits "A" on all 5 nodes');
  n0.propose('A');
  c.pump();
  console.log(`    applied everywhere: [${c.appliedLogs()[0].map(show)}]`);

  console.log('[2] lease valid: "B1" commits with Phase 2 only (no Prepare)');
  const prepares = c.network.countSent('prepare');
  n0.propose('B1');
  c.pump();
  console.log(`    prepare messages sent: ${c.network.countSent('prepare') - prepares} (skipped Phase 1)`);

  console.log('[3] partition {0,1} | {2,3,4}; old leader writes dirty entries in minority');
  c.network.partition([0, 1], [2, 3, 4]);
  n0.propose('DIRTY');
  c.pump();
  console.log(`    node0 commitUpTo=${n0.commitUpTo} (cannot commit without majority)`);

  console.log('[4] majority side elects node2 via Phase 1, commits "C"');
  n2.propose('C');
  c.pump();
  console.log(`    node2 isLeader=${n2.isLeader}, commitUpTo=${n2.commitUpTo}`);

  console.log('[5] heal: node2 replicates; dirty entry on {0,1} is overwritten');
  c.network.healAll();
  n2.replicate();
  c.pump();
  console.log(`    node0 slot3 value now: ${show(n0.acceptedLog.get(3)!.value)}, isLeader=${n0.isLeader}`);
  for (const n of c.nodes) {
    console.log(`    node${n.id} applied: [${n.applied.map((a) => show(a.value)).join(', ')}]`);
  }
  console.log(`    network leak-free: ${c.network.leakFree} (sent=${c.network.stats.sent}, delivered=${c.network.stats.delivered}, dropped=${c.network.stats.dropped})`);
}

await queueDemo();
paxosDemo();
