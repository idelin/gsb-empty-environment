import { DVVStore, VersionVector, canonicalize, entrySize } from "../src/index.ts";
import type { DVVEntry } from "../src/index.ts";

const KEY = "profile";

function show<T>(label: string, store: DVVStore<T>): void {
  const snap = store.snapshot(KEY);
  const siblings = snap.siblings
    .map((s) => `(${s.dot.node},${s.dot.counter})=${JSON.stringify(s.value)}`)
    .join(" ");
  console.log(
    `  ${label.padEnd(34)} ctx=${JSON.stringify(snap.context).padEnd(24)} siblings=[${siblings}] size=${entrySize(store.entry(KEY))}`,
  );
}

function broadcast<T>(from: DVVStore<T>, to: DVVStore<T>[], copies = 1): void {
  const entry = from.entry(KEY);
  for (const replica of to) {
    for (let i = 0; i < copies; i += 1) {
      replica.merge(KEY, entry);
    }
  }
}

function shuffle<T>(items: T[], rand: () => number): T[] {
  const copy = [...items];
  for (let i = copy.length - 1; i > 0; i -= 1) {
    const j = Math.floor(rand() * (i + 1));
    [copy[i], copy[j]] = [copy[j], copy[i]];
  }
  return copy;
}

let seed = 7;
const rand = (): number => {
  seed = (seed * 1103515245 + 12345) % 2147483648;
  return seed / 2147483648;
};

console.log("=== DVV dedup demo: leaderless replication, key =", JSON.stringify(KEY), "===\n");

const A = new DVVStore<string>("A");
const B = new DVVStore<string>("B");
const C = new DVVStore<string>("C");
const all = [A, B, C];

console.log("[1] A writes 'alice', broadcast to B and C");
A.put(KEY, "alice");
broadcast(A, [B, C]);
all.forEach((r) => show(`replica ${r.nodeId}`, r));

console.log("\n[2] 50 empty updates on A (same value, causal rewrites)");
let ctx: VersionVector = A.get(KEY).context;
for (let i = 0; i < 50; i += 1) {
  ctx = A.put(KEY, "alice", ctx).context;
}
show("replica A after 50 rewrites", A);

console.log("\n[3] retry storm: B rewrites 'alice' 5x with a STALE read context");
const stale = B.get(KEY).context;
for (let i = 0; i < 5; i += 1) {
  B.put(KEY, "alice", stale);
}
show("replica B (no ghost branches)", B);

console.log("\n[4] concurrent conflict: B writes 'bob', C writes 'carol' (disjoint contexts)");
B.put(KEY, "bob", B.get(KEY).context);
C.put(KEY, "carol", C.get(KEY).context);
show("replica B", B);
show("replica C", C);

console.log("\n[5] out-of-order + duplicated gossip (3 rounds, shuffled, 2x copies)");
for (let round = 0; round < 3; round += 1) {
  const entries: DVVEntry<string>[] = all.map((r) => r.entry(KEY));
  for (const entry of shuffle(entries, rand)) {
    for (const replica of shuffle(all, rand)) {
      replica.merge(KEY, entry);
      replica.merge(KEY, entry);
    }
  }
}
all.forEach((r) => show(`replica ${r.nodeId}`, r));

console.log("\n[6] A resolves the conflict with a causal overwrite -> 'alice-2026'");
A.put(KEY, "alice-2026", A.get(KEY).context);
broadcast(A, [B, C], 2);
all.forEach((r) => show(`replica ${r.nodeId}`, r));

const snapshots = all.map((r) => canonicalize(r.snapshot(KEY)));
const converged = snapshots.every((s) => s === snapshots[0]);
const finalEntry = A.entry(KEY);
console.log("\nconverged:", converged);
console.log("final values:", JSON.stringify(A.get(KEY).values));
console.log(`version graph size: ${entrySize(finalEntry)} (context entries=${finalEntry.context.size}, siblings=${finalEntry.siblings.length})`);
if (!converged || finalEntry.siblings.length !== 1) {
  process.exitCode = 1;
}
