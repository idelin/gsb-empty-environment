/**
 * Worker entry for the stress scenario.
 *  - role "writer": single thread, dense multi-key transfer transactions.
 *  - role "reader": read-only thread, continuous full-table checksum scans.
 */
import { parentPort, workerData } from "node:worker_threads";
import { attachStore, MvccReader, MvccWriter, CTRL_DONE, NIL } from "./mvcc.ts";

interface WorkerArgs {
  sab: SharedArrayBuffer;
  keyCount: number;
  poolSize: number;
  role: "writer" | "reader";
  txCount: number;
  expectedSum: number;
  seed: number;
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const args = workerData as WorkerArgs;
const store = attachStore(args.sab, args.keyCount, args.poolSize);

if (args.role === "writer") {
  const writer = new MvccWriter(store);
  const rand = mulberry32(args.seed);
  const started = process.hrtime.bigint();
  for (let tx = 0; tx < args.txCount; tx++) {
    // Transfer a random integer amount between two distinct random keys.
    // Integer arithmetic keeps the global sum exactly conserved in f64.
    const from = Math.floor(rand() * args.keyCount);
    let to = Math.floor(rand() * args.keyCount);
    if (to === from) to = (to + 1) % args.keyCount;
    const amount = 1 + Math.floor(rand() * 100);

    // Single writer: latest committed value is always at the chain head.
    const headFrom = Atomics.load(store.heads, from);
    const headTo = Atomics.load(store.heads, to);
    const valFrom = headFrom === NIL ? store.baseVal[from] : store.nodeVal[headFrom];
    const valTo = headTo === NIL ? store.baseVal[to] : store.nodeVal[headTo];

    writer.begin();
    writer.put(from, valFrom - amount);
    writer.put(to, valTo + amount);
    writer.commit();
  }
  const elapsedNs = process.hrtime.bigint() - started;
  Atomics.store(store.control, CTRL_DONE, 1);
  parentPort!.postMessage({
    role: "writer",
    txCommitted: args.txCount,
    elapsedMs: Number(elapsedNs) / 1e6,
  });
} else {
  const reader = new MvccReader(store);
  let scans = 0;
  let violations = 0;
  const started = process.hrtime.bigint();
  while (Atomics.load(store.control, CTRL_DONE) === 0) {
    const snap = reader.snapshot();
    const sum = reader.checksum(snap);
    scans++;
    if (sum !== args.expectedSum) {
      violations++;
      parentPort!.postMessage({ role: "reader", torn: { snap, sum } });
    }
  }
  const elapsedNs = process.hrtime.bigint() - started;
  parentPort!.postMessage({
    role: "reader",
    scans,
    violations,
    elapsedMs: Number(elapsedNs) / 1e6,
  });
}
