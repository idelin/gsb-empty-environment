import { LSEQSequence, mulberry32, type InsertOp } from "../src/lseq.ts";

// --- Scenario 1: dense alternating front/back typing on one replica -------
const solo = new LSEQSequence(1, mulberry32(2024));
for (let i = 0; i < 1000; i++) {
  const ch = String.fromCharCode(33 + (i % 94));
  solo.insert(i % 2 === 0 ? 0 : solo.length, ch);
}
const s = solo.stats();
console.log("[dense alternating inserts x1000]");
console.log(`  size=${s.size}  maxDepth=${s.maxDepth}  meanDepth=${s.meanDepth.toFixed(2)}`);
console.log(`  (log2(1000) ~= 9.97 -> depth growth is poly-logarithmic, not linear)`);

// --- Scenario 2: three replicas edit, a late joiner merges in random order -
const sites = [1, 2, 3].map((site) => new LSEQSequence(site, mulberry32(site * 101)));
const ops: InsertOp[] = [];
const words = ["alpha", "beta", "gamma"];
for (const [i, replica] of sites.entries()) {
  const rng = mulberry32(7000 + i);
  for (const ch of words[i]) {
    ops.push(replica.insert(Math.floor(rng() * (replica.length + 1)), ch));
  }
}
const late = new LSEQSequence(9, mulberry32(9));
late.merge([...ops].sort(() => mulberry32(5)() - 0.5));
console.log("\n[3 replicas + late joiner, random-order merge]");
console.log(`  merged=${late.length} ops  sequence="${late.toString()}"`);
console.log(`  identifiers (depths): ${late.identifiers().map((id) => id.length).join(",")}`);
