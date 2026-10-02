/**
 * Demo: encode 150 variable-length string keys into a single 4KB
 * slotted page (leaf node), then locate every key via binary search.
 *
 * Run: npm run demo
 */
import {
  HEADER_SIZE,
  InternalPage,
  LeafPage,
  PAGE_SIZE,
  SLOT_SIZE,
} from "./slotted-page.ts";

const KEY_COUNT = 150;

/** Deterministic variable-length keys, 14-18 bytes each. */
function makeKey(i: number): string {
  return `user:${String(i).padStart(8, "0")}:${"x".repeat(i % 5)}`;
}

// ---------------------------------------------------------------- leaf page
console.log("=== LeafPage: 150 variable-length keys in one 4KB page ===\n");

const leaf = new LeafPage();
const t0 = performance.now();
// Insert in shuffled (non-sorted) order; the slot directory stays sorted.
const order = Array.from({ length: KEY_COUNT }, (_, i) => (i * 67) % KEY_COUNT);
for (const i of order) {
  leaf.insert(makeKey(i), { pageId: 1000 + i, slotId: i % 64 });
}
const insertMs = performance.now() - t0;

console.log(`inserted        : ${leaf.slotCount} keys (${insertMs.toFixed(2)} ms)`);
console.log(`page size       : ${PAGE_SIZE} bytes`);
console.log(`header          : ${HEADER_SIZE} bytes`);
console.log(`slot directory  : [${HEADER_SIZE}, ${leaf.freeStart})  (${leaf.slotCount} slots x ${SLOT_SIZE}B, grows --->)`);
console.log(`record area     : [${leaf.freeEnd}, ${PAGE_SIZE})  (<--- grows, keys + 6B RID)`);
console.log(`free space      : ${leaf.freeSpace} bytes (middle gap)`);

// Verify the directory is sorted ascending.
let sorted = true;
let prev: Buffer | null = null;
for (const { key } of leaf.entries()) {
  if (prev !== null && prev.compare(key) >= 0) sorted = false;
  prev = Buffer.from(key);
}
console.log(`directory sorted: ${sorted}`);

// Locate every key with binary search and track the probe counts.
const t1 = performance.now();
let maxProbes = 0;
let totalProbes = 0;
for (let i = 0; i < KEY_COUNT; i++) {
  const hit = leaf.lookup(makeKey(i));
  if (!hit) throw new Error(`missing key ${makeKey(i)}`);
  maxProbes = Math.max(maxProbes, leaf.lastProbeCount);
  totalProbes += leaf.lastProbeCount;
}
const lookupMs = performance.now() - t1;

console.log(`\nlookup all ${KEY_COUNT} keys: ${lookupMs.toFixed(2)} ms (${((lookupMs * 1000) / KEY_COUNT).toFixed(1)} us/key)`);
console.log(`binary search probes: max ${maxProbes}, avg ${(totalProbes / KEY_COUNT).toFixed(2)} (ceil(log2(${KEY_COUNT})) = ${Math.ceil(Math.log2(KEY_COUNT))})`);

// Show a few concrete lookups with their slot index and page offset.
console.log("\nsample lookups:");
for (const i of [0, 1, 74, 149]) {
  const hit = leaf.lookup(makeKey(i))!;
  console.log(
    `  ${JSON.stringify(makeKey(i))} -> slot #${String(hit.slot).padStart(3)}  ` +
      `record@offset ${hit.offset}  rid=(page ${hit.rid.pageId}, slot ${hit.rid.slotId})  probes=${leaf.lastProbeCount}`,
  );
}

// ------------------------------------------------------------ internal page
console.log("\n=== InternalPage: separator keys -> child page ids ===\n");

const internal = new InternalPage();
internal.setLeftmostChild(0);
for (let i = 1; i <= 10; i++) {
  internal.insert(`k${String(i * 10).padStart(3, "0")}`, i);
}
console.log(`separators      : ${internal.slotCount} (leftmost child = page ${internal.leftmostChild})`);
for (const probe of ["k005", "k010", "k015", "k055", "k100", "k999"]) {
  const child = internal.findChild(probe);
  console.log(`  findChild(${JSON.stringify(probe)}) -> page ${child}  (probes=${internal.lastProbeCount})`);
}
