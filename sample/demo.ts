import { SkipList } from "../src/skiplist.ts";

const list = new SkipList<number, string>();

// 1) Insert 10,000 random integer key/value pairs.
const inserted = new Set<number>();
for (let i = 0; i < 10_000; i++) {
  const key = Math.floor(Math.random() * 40_000);
  list.insert(key, `value-${i}`);
  inserted.add(key);
}
console.log(`inserted 10,000 random pairs -> ${list.size} unique keys`);

// 2) Range scan: strictly increasing output.
const lo = 10_000;
const hi = 10_020;
const range = list.entries({ gte: lo, lte: hi });
console.log(`range scan [${lo}, ${hi}] ->`, range.map((e) => e.key));

// 3) Snapshot isolation: iterate while a concurrent writer inserts.
const snapshot = list.iterator({ gte: 0, lte: 100 });
const writer = (async () => {
  for (let i = 0; i <= 100; i++) {
    list.insert(i, `late-${i}`); // keys inside the scanned range!
    await new Promise((r) => setImmediate(r));
  }
})();

const stable: number[] = [];
while (true) {
  const { done, value } = snapshot.next();
  if (done) break;
  stable.push(value.key);
  await new Promise((r) => setImmediate(r)); // let the writer interleave
}
await writer;

console.log(`snapshot scan [0, 100] saw ${stable.length} keys (frozen at creation)`);
console.log(`fresh scan of same range now sees ${list.entries({ gte: 0, lte: 100 }).length} keys`);
