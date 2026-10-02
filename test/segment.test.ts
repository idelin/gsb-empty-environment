import test from "node:test";
import assert from "node:assert/strict";
import { SegmentBuilder, SegmentReader, tokenize } from "../src/segment.ts";
import { intersectAnd } from "../src/intersect.ts";

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

test("tokenize lowercases and splits on non-alphanumerics", () => {
  assert.deepEqual(tokenize("Database, STORAGE-engine v2!"), [
    "database",
    "storage",
    "engine",
    "v2",
  ]);
  assert.deepEqual(tokenize(""), []);
});

test("segment round-trip: postings match the source documents", () => {
  const docs = [
    "the quick brown fox",
    "database storage engine",
    "storage storage storage",
    "database index",
    "a database in a storage system",
  ];
  const builder = new SegmentBuilder();
  docs.forEach((d, i) => assert.equal(builder.addDocument(d), i));
  const reader = new SegmentReader(builder.build());

  assert.equal(reader.docCount, docs.length);

  const storage = reader.postings("storage")!;
  const seen: [number, number][] = [];
  while (!storage.done()) {
    seen.push([storage.docId(), storage.tf()]);
    storage.next();
  }
  assert.deepEqual(seen, [
    [1, 1],
    [2, 3],
    [4, 1],
  ]);

  assert.equal(reader.postings("nonexistent"), null);
  assert.equal(reader.lookup("database")!.docFreq, 3);
});

test("AND intersection equals naive set intersection on random data", () => {
  const rand = mulberry32(1234);
  const vocab = Array.from({ length: 40 }, (_, i) => `w${i}`);
  const builder = new SegmentBuilder();
  const docTerms: Set<string>[] = [];
  for (let d = 0; d < 500; d++) {
    const terms = vocab.filter(() => rand() < 0.1);
    docTerms.push(new Set(terms));
    builder.addDocument(terms.join(" "));
  }
  const reader = new SegmentReader(builder.build());

  for (let trial = 0; trial < 50; trial++) {
    const terms = vocab.filter(() => rand() < 0.15);
    if (terms.length === 0) continue;
    const { docIds } = intersectAnd(terms, (t) => reader.postings(t));
    const expected = docTerms
      .map((set, docId) => (terms.every((t) => set.has(t)) ? docId : -1))
      .filter((docId) => docId >= 0);
    assert.deepEqual(docIds, expected, `terms=${terms.join(",")}`);
  }
});

test("acceptance: 'database AND storage' with skewed posting lists", () => {
  // Long list: "storage" in ~5/6 of 50000 docs (~41k postings).
  // Short list: "database" in 30 docs, grouped in 3 remote clusters, so the
  // short list drives the intersection and skip pointers leap over the long
  // list's dead regions between clusters.
  const rand = mulberry32(42);
  const builder = new SegmentBuilder();
  const expectedMatches: number[] = [];
  const DOC_COUNT = 50000;
  const databaseDocs = new Set<number>();
  for (const clusterStart of [5000, 25000, 48000]) {
    for (let i = 0; i < 10; i++) databaseDocs.add(clusterStart + i);
  }
  for (let docId = 0; docId < DOC_COUNT; docId++) {
    const words: string[] = [];
    const hasStorage = rand() < 5 / 6;
    const hasDatabase = databaseDocs.has(docId);
    if (hasStorage) words.push("storage");
    if (hasDatabase) words.push("database");
    if (hasStorage && hasDatabase) expectedMatches.push(docId);
    builder.addDocument(words.join(" "));
  }
  const reader = new SegmentReader(builder.build());

  const storageFreq = reader.lookup("storage")!.docFreq;
  const databaseFreq = reader.lookup("database")!.docFreq;
  assert.equal(databaseFreq, 30);
  assert.ok(
    storageFreq > databaseFreq * 100,
    `expected skewed lists, got storage=${storageFreq} database=${databaseFreq}`,
  );

  const terms = ["database", "storage"];
  const withSkips = intersectAnd(terms, (t) => reader.postings(t), true);
  const baseline = intersectAnd(terms, (t) => reader.postings(t), false);

  // Correctness: exact intersection, identical to the naive baseline.
  assert.deepEqual(withSkips.docIds, expectedMatches);
  assert.deepEqual(withSkips.docIds, baseline.docIds);

  // The short list drives: it is fully scanned.
  assert.equal(withSkips.stats.decodedPerTerm["database"], databaseFreq);

  // Skip pointers engaged on the long list...
  assert.ok(
    withSkips.stats.skipsPerTerm["storage"] > 0,
    "expected skip jumps on the long list",
  );
  // ...and the long list decodes far fewer postings than its length.
  assert.ok(
    withSkips.stats.decodedPerTerm["storage"] < storageFreq / 10,
    `long list decoded ${withSkips.stats.decodedPerTerm["storage"]} of ${storageFreq}`,
  );
  // The naive baseline decodes (nearly) the whole long list.
  assert.ok(baseline.stats.decodedPerTerm["storage"] > storageFreq * 0.8);
});

test("intersection with a missing term is empty", () => {
  const builder = new SegmentBuilder();
  builder.addDocument("database storage");
  const reader = new SegmentReader(builder.build());
  const { docIds, stats } = intersectAnd(["database", "ghost"], (t) =>
    reader.postings(t),
  );
  assert.deepEqual(docIds, []);
  assert.equal(stats.totalDecoded, 0);
});

test("reader rejects corrupt input", () => {
  const builder = new SegmentBuilder();
  builder.addDocument("hello");
  const bytes = builder.build();
  bytes[0] ^= 0xff;
  assert.throws(() => new SegmentReader(bytes), /bad segment magic/);
});
