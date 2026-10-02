/**
 * Demo: build a compact read-only inverted index segment, then evaluate
 * "database AND storage" where the two posting lists are highly skewed
 * ("storage" ~ 5000 postings, "database" ~ 30). The short list drives the
 * intersection and skip pointers let the long list leap over dead regions.
 *
 * Run: node demo.ts   (Node >= 22.6 with type stripping; Node >= 23.6 natively)
 */

import { SegmentBuilder, SegmentReader } from "./src/segment.ts";
import { intersectAnd, type IntersectResult } from "./src/intersect.ts";

// Deterministic PRNG (mulberry32) so the demo output is stable.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const FILLER = [
  "query", "index", "cache", "vector", "search", "engine", "schema",
  "buffer", "commit", "segment", "replica", " compaction", "token",
];

function buildCorpus(): string[] {
  const rand = mulberry32(42);
  const docs: string[] = [];
  const DOC_COUNT = 50000;
  // "database" is rare: 30 documents in 3 remote clusters, so the short
  // posting list drives the intersection across dead regions of the long one.
  const databaseDocs = new Set<number>();
  for (const clusterStart of [5000, 25000, 48000]) {
    for (let i = 0; i < 10; i++) databaseDocs.add(clusterStart + i);
  }
  for (let docId = 0; docId < DOC_COUNT; docId++) {
    const words: string[] = [];
    // "storage" is common: present in ~5/6 of all documents.
    if (rand() < 5 / 6) words.push("storage", "storage engine");
    if (databaseDocs.has(docId)) words.push("database", "database", "database");
    // Filler terms to make the dictionary non-trivial.
    for (const word of FILLER) {
      if (rand() < 0.08) words.push(word.trim());
    }
    docs.push(words.join(" "));
  }
  return docs;
}

function report(label: string, result: IntersectResult): void {
  const { docIds, stats } = result;
  console.log(`\n[${label}]`);
  console.log(`  matches        : ${docIds.length} docs`);
  console.log(
    `  first matches  : [${docIds.slice(0, 8).join(", ")}${docIds.length > 8 ? ", ..." : ""}]`,
  );
  for (const [term, decoded] of Object.entries(stats.decodedPerTerm)) {
    console.log(
      `  decoded[${term}]`.padEnd(24) +
        `: ${decoded} postings, ${stats.skipsPerTerm[term]} skip jumps`,
    );
  }
  console.log(`  total decoded  : ${stats.totalDecoded} postings`);
}

const docs = buildCorpus();
const builder = new SegmentBuilder();
for (const doc of docs) builder.addDocument(doc);
const segmentBytes = builder.build();

const reader = new SegmentReader(segmentBytes);
const storageEntry = reader.lookup("storage")!;
const databaseEntry = reader.lookup("database")!;

console.log("=== compact read-only inverted index segment ===");
console.log(`documents        : ${reader.docCount}`);
console.log(`terms            : ${reader.termCount}`);
console.log(`segment size     : ${segmentBytes.length} bytes`);
console.log(
  `posting lists    : "storage" docFreq=${storageEntry.docFreq} (${storageEntry.postingsLength} bytes), ` +
    `"database" docFreq=${databaseEntry.docFreq} (${databaseEntry.postingsLength} bytes)`,
);

const terms = ["database", "storage"];
console.log(`\nquery: ${terms.join(" AND ").toUpperCase()}`);

const withSkips = intersectAnd(terms, (t) => reader.postings(t), true);
report("skip-pointer intersection", withSkips);

const baseline = intersectAnd(terms, (t) => reader.postings(t), false);
report("naive linear intersection (baseline)", baseline);

const same =
  withSkips.docIds.length === baseline.docIds.length &&
  withSkips.docIds.every((id, i) => id === baseline.docIds[i]);
console.log(`\nresults identical: ${same}`);
console.log(
  `long-list decode reduction: ${baseline.stats.decodedPerTerm["storage"]} -> ` +
    `${withSkips.stats.decodedPerTerm["storage"]} postings ` +
    `(${((1 - withSkips.stats.decodedPerTerm["storage"] / baseline.stats.decodedPerTerm["storage"]) * 100).toFixed(1)}% fewer)`,
);
