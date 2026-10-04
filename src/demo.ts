import { DeadlockDetector } from './detector';
import { GlobalWaitGraph } from './globalGraph';
import { LocalPartition } from './partition';
import { formatCycle } from './detector';
import { EdgeMessage } from './types';

// End-to-end demo: two partitions, cross-partition deadlock, out-of-order network.
const graph = new GlobalWaitGraph();
const inbox: EdgeMessage[] = [];
const alive = new Set<string>(['T1', 'T2', 'T3']);

const p1 = new LocalPartition('P1', (m) => inbox.push(m));
const p2 = new LocalPartition('P2', (m) => inbox.push(m));

const detector = new DeadlockDetector(graph, {
  isAlive: (t) => alive.has(t),
  abortCost: (t) => (t === 'T3' ? 1 : 10),
});

for (const t of ['T1', 'T3']) p1.begin(t);
p2.begin('T2');

// T1 -> T2 (P1), T2 -> T3 (P2), T3 -> T1 (P1): each local graph stays acyclic.
p1.addWait('T1', 'T2');
p2.addWait('T2', 'T3');
p1.addWait('T3', 'T1');

// Deliver in reverse order to simulate reordering; stale duplicates are dropped.
for (const msg of [...inbox].reverse()) graph.apply(msg);
inbox.length = 0;

const d = detector.diagnose();
if (d.status === 'deadlock') {
  console.log('deadlock detected, shortest cycle:');
  console.log(formatCycle(d.cycle));
  console.log('suggestion:', d.suggestion?.reason ?? '(no live victim)');
} else {
  console.log('status:', d.status);
}
