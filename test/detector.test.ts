import assert from 'node:assert';
import {
  DeadlockDetector,
  EdgeMessage,
  GlobalWaitGraph,
  LocalPartition,
  findShortestCycle,
  formatCycle,
} from '../src';

/** A network buffer that can hold, reorder and duplicate in-flight messages. */
class Network {
  private queue: EdgeMessage[] = [];
  readonly delivered: EdgeMessage[] = [];

  send = (msg: EdgeMessage): void => {
    this.queue.push(msg);
  };

  /** Deliver queued messages in the given order of indices (default: FIFO). */
  flush(order?: number[]): void {
    const idx = order ?? this.queue.map((_, i) => i);
    const next: EdgeMessage[] = [];
    for (const i of idx) this.deliver(this.queue[i]!);
    for (let i = 0; i < this.queue.length; i++) {
      if (!idx.includes(i)) next.push(this.queue[i]!);
    }
    this.queue = next;
  }

  private deliver(msg: EdgeMessage): void {
    this.delivered.push(msg);
    this.graph.apply(msg);
  }

  constructor(private readonly graph: GlobalWaitGraph) {}
}

interface Harness {
  graph: GlobalWaitGraph;
  net: Network;
  detector: DeadlockDetector;
  p1: LocalPartition;
  p2: LocalPartition;
  alive: Set<string>;
}

function makeHarness(): Harness {
  const graph = new GlobalWaitGraph();
  const net = new Network(graph);
  const alive = new Set<string>();
  const mk = (id: string) =>
    new LocalPartition(id, (m) => {
      net.send(m);
    });
  const p1 = mk('P1');
  const p2 = mk('P2');
  const detector = new DeadlockDetector(graph, { isAlive: (t) => alive.has(t) });
  return { graph, net, detector, p1, p2, alive };
}

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    console.error(`FAIL - ${name}`);
    throw err;
  }
}

// 1. Local graphs are individually acyclic, yet a global deadlock exists.
test('global cycle detected while every local graph is acyclic', () => {
  const h = makeHarness();
  h.alive.add('T1').add('T2');
  h.p1.begin('T1');
  h.p2.begin('T2');
  // T1 (on P1) waits for T2; T2 (on P2) waits for T1. Cross-partition edges.
  h.p1.addWait('T1', 'T2');
  h.p2.addWait('T2', 'T1');
  assert.strictEqual(h.p1.hasLocalCycle(), false);
  assert.strictEqual(h.p2.hasLocalCycle(), false);
  h.net.flush();

  const d = h.detector.diagnose();
  assert.strictEqual(d.status, 'deadlock');
  if (d.status !== 'deadlock') return;
  assert.deepStrictEqual([...d.cycle.nodes].sort(), ['T1', 'T2']);
  assert.strictEqual(d.cycle.edges.length, 2);
  // Edge versions are reported.
  const rendered = formatCycle(d.cycle);
  assert.match(rendered, /T1 --\[v\d+@P1\]--> T2/);
  assert.match(rendered, /T2 --\[v\d+@P2\]--> T1/);
});

// 2. Local partition refuses an edge that would create a local cycle.
test('local partition rejects locally-cyclic wait edge', () => {
  const h = makeHarness();
  h.p1.begin('A');
  h.p1.begin('B');
  h.p1.addWait('A', 'B');
  assert.throws(() => h.p1.addWait('B', 'A'), /local cycle/);
});

// 3. Out-of-order remove/add must not resurrect a stale edge.
test('out-of-order delivery never resurrects a removed edge', () => {
  const h = makeHarness();
  h.alive.add('T1').add('T2');
  h.p1.begin('T1');
  h.p2.begin('T2');
  h.p1.addWait('T1', 'T2'); // add@v1
  h.p2.addWait('T2', 'T1'); // add@v1 (P2 clock)
  h.net.flush();
  assert.strictEqual(h.detector.diagnose().status, 'deadlock');

  // T1's lock is granted: P1 removes its edge (remove@v2), but the network
  // reorders: an old duplicate of add@v1 arrives AFTER remove@v2.
  h.p1.removeWait('T1', 'T2');
  const addV1: EdgeMessage = { kind: 'add', from: 'T1', to: 'T2', version: 1, partition: 'P1' };
  h.net.flush(); // deliver remove@v2 first
  h.graph.apply(addV1); // stale add arrives late
  h.graph.apply(addV1); // ...and even twice

  assert.strictEqual(h.graph.hasEdge('T1', 'T2'), false);
  assert.strictEqual(h.detector.diagnose().status, 'no-deadlock');
});

// 4. Duplicate delivery of the same message is idempotent.
test('duplicate messages are ignored', () => {
  const h = makeHarness();
  h.p1.begin('T1');
  h.p1.begin('T2');
  h.p1.addWait('T1', 'T2');
  h.net.flush();
  const before = h.graph.epoch;
  const dup: EdgeMessage = { kind: 'add', from: 'T1', to: 'T2', version: 1, partition: 'P1' };
  assert.strictEqual(h.graph.apply(dup), false);
  assert.strictEqual(h.graph.epoch, before);
  assert.strictEqual(h.graph.hasEdge('T1', 'T2', 1), true);
});

// 5. Shortest cycle is reported when several cycles exist.
test('shortest cycle wins among multiple cycles', () => {
  const h = makeHarness();
  // A, C, D wait from P1; B, E wait from P2 (keeps both local graphs acyclic).
  for (const t of ['A', 'C', 'D']) h.p1.begin(t);
  for (const t of ['B', 'E']) h.p2.begin(t);
  for (const t of ['A', 'B', 'C', 'D', 'E']) h.alive.add(t);
  // 3-cycle: A -> B -> C -> A ; 4-cycle: A -> B -> D -> E -> A
  h.p1.addWait('A', 'B');
  h.p2.addWait('B', 'C');
  h.p1.addWait('C', 'A');
  h.p2.addWait('B', 'D');
  h.p1.addWait('D', 'E');
  h.p2.addWait('E', 'A');
  h.net.flush();

  const cycle = findShortestCycle(h.graph.snapshotEdges())!;
  assert.strictEqual(cycle.nodes.length, 3);
  assert.deepStrictEqual([...cycle.nodes].sort(), ['A', 'B', 'C']);
  // Every edge carries its version.
  for (const e of cycle.edges) assert.ok(e.version > 0);
});

// 6. Abort suggestion only targets transactions that still exist.
test('victim is chosen only among live transactions', () => {
  const h = makeHarness();
  h.alive.add('T1').add('T2').add('T3');
  h.p1.begin('T1');
  h.p2.begin('T2');
  h.p1.begin('T3');
  h.p1.addWait('T1', 'T2');
  h.p2.addWait('T2', 'T3');
  h.p1.addWait('T3', 'T1');
  h.net.flush();

  // T1 already committed before the diagnosis ran.
  h.alive.delete('T1');
  const d = h.detector.diagnose();
  assert.strictEqual(d.status, 'deadlock');
  if (d.status !== 'deadlock') return;
  assert.ok(d.suggestion);
  assert.notStrictEqual(d.suggestion!.victim, 'T1');
  assert.ok(['T2', 'T3'].includes(d.suggestion!.victim));
});

// 7. Cheapest live victim is preferred.
test('cheapest live transaction is suggested as victim', () => {
  const graph = new GlobalWaitGraph();
  const alive = new Set(['T1', 'T2', 'T3']);
  const costs: Record<string, number> = { T1: 100, T2: 1, T3: 50 };
  const detector = new DeadlockDetector(graph, {
    isAlive: (t) => alive.has(t),
    abortCost: (t) => costs[t] ?? 1,
  });
  graph.applyAll([
    { kind: 'add', from: 'T1', to: 'T2', version: 1, partition: 'P1' },
    { kind: 'add', from: 'T2', to: 'T3', version: 2, partition: 'P1' },
    { kind: 'add', from: 'T3', to: 'T1', version: 3, partition: 'P1' },
  ]);
  const d = detector.diagnose();
  assert.strictEqual(d.status, 'deadlock');
  if (d.status === 'deadlock') assert.strictEqual(d.suggestion?.victim, 'T2');
});

// 8. No suggestion when every transaction on the cycle is gone.
test('no abort suggestion when no live transaction remains', () => {
  const h = makeHarness();
  h.p1.begin('T1');
  h.p2.begin('T2');
  h.p1.addWait('T1', 'T2');
  h.p2.addWait('T2', 'T1');
  h.net.flush();
  // Both transactions finished elsewhere; edges linger briefly at coordinator.
  const d = h.detector.diagnose();
  assert.strictEqual(d.status, 'deadlock');
  if (d.status === 'deadlock') assert.strictEqual(d.suggestion, null);
});

// 9. A stale snapshot must not trigger a kill (no false abort).
test('stale snapshot never produces an abort suggestion', () => {
  const h = makeHarness();
  h.alive.add('T1').add('T2');
  h.p1.begin('T1');
  h.p2.begin('T2');
  h.p1.addWait('T1', 'T2');
  h.p2.addWait('T2', 'T1');
  h.net.flush();

  const snap = h.detector.snapshot(); // snapshot captures the deadlock
  // Before diagnosis, the deadlock resolves: T1 releases its wait on T2.
  h.p1.removeWait('T1', 'T2');
  h.net.flush();

  const d = h.detector.diagnose(snap);
  assert.strictEqual(d.status, 'stale-snapshot');
  // Fresh diagnosis agrees there is no deadlock anymore.
  assert.strictEqual(h.detector.diagnose().status, 'no-deadlock');
});

// 10. Snapshot stays valid when nothing changed.
test('fresh snapshot diagnoses deadlock and suggests a live victim', () => {
  const h = makeHarness();
  h.alive.add('T1').add('T2');
  h.p1.begin('T1');
  h.p2.begin('T2');
  h.p1.addWait('T1', 'T2');
  h.p2.addWait('T2', 'T1');
  h.net.flush();
  const snap = h.detector.snapshot();
  const d = h.detector.diagnose(snap);
  assert.strictEqual(d.status, 'deadlock');
  if (d.status === 'deadlock') assert.ok(d.suggestion);
});

// 11. Delayed (not reordered) delivery: diagnosis on partial view is safe.
test('partial view with in-flight messages does not hallucinate a cycle', () => {
  const h = makeHarness();
  h.alive.add('T1').add('T2');
  h.p1.begin('T1');
  h.p2.begin('T2');
  h.p1.addWait('T1', 'T2');
  h.p2.addWait('T2', 'T1');
  h.net.flush([0]); // only the first edge delivered so far
  assert.strictEqual(h.detector.diagnose().status, 'no-deadlock');
  h.net.flush(); // second edge arrives
  assert.strictEqual(h.detector.diagnose().status, 'deadlock');
});

console.log('all tests passed');
