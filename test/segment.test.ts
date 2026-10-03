import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { ByteReader, ByteWriter } from '../src/bytes.ts';
import { SKIP_INTERVAL } from '../src/postings.ts';
import { Segment, SegmentBuilder } from '../src/segment.ts';

test('varint round-trips boundary values', () => {
  const values = [0, 1, 127, 128, 300, 16384, 2 ** 31 - 1, 2 ** 32, 2 ** 45];
  const writer = new ByteWriter();
  for (const v of values) writer.writeVarint(v);
  const reader = new ByteReader(writer.toUint8Array(), 0);
  for (const v of values) assert.equal(reader.readVarint(), v);
});

/** Corpus with heavily skewed posting-list lengths for 'database' vs 'storage'. */
function buildSkewedCorpus(): {
  segment: Segment;
  databaseDocs: Set<number>;
  storageDocs: Set<number>;
  indexDocs: Set<number>;
} {
  const builder = new SegmentBuilder();
  const databaseDocs = new Set<number>();
  const storageDocs = new Set<number>();
  const indexDocs = new Set<number>();

  // 'storage' appears in 3000 documents (long chain).
  for (let docId = 1; docId <= 3000; docId++) {
    const tokens = ['storage', 'engine', `filler${docId % 50}`];
    if (docId % 7 === 0) tokens.push('index');
    if (docId % 7 === 0) indexDocs.add(docId);
    builder.addDocument(docId, tokens);
    storageDocs.add(docId);
  }
  // 'database' appears in only 12 documents (short chain), some also containing 'storage'.
  const dbIds = [3, 77, 512, 1000, 1024, 1500, 2048, 2500, 2999, 3000, 4001, 5000];
  for (const docId of dbIds) {
    const tokens = ['database', 'database', 'kernel'];
    if (storageDocs.has(docId)) tokens.push('storage');
    if (docId % 7 === 0) tokens.push('index');
    builder.addDocument(docId, tokens);
    databaseDocs.add(docId);
    if (docId % 7 === 0) indexDocs.add(docId);
  }

  return {
    segment: new Segment(builder.build()),
    databaseDocs,
    storageDocs,
    indexDocs,
  };
}

test('dictionary finds every term and rejects unknown terms', () => {
  const builder = new SegmentBuilder();
  const terms = ['data', 'database', 'datum', 'storage', 'store', 'stream', 'index'];
  terms.forEach((term, i) => builder.addDocument(i + 1, [term]));
  const segment = new Segment(builder.build());

  assert.deepEqual(segment.terms(), [...terms].sort());
  terms.forEach((term, i) => {
    const info = segment.termInfo(term);
    assert.ok(info, `term ${term} should exist`);
    assert.equal(info.docCount, 1);
    const it = segment.lookup(term)!;
    assert.equal(it.next(), i + 1);
  });
  assert.equal(segment.termInfo('db'), null);
  assert.equal(segment.termInfo('databases'), null);
  assert.equal(segment.lookup('missing'), null);
});

test('posting lists preserve docIDs (delta) and term frequencies', () => {
  const builder = new SegmentBuilder();
  builder.addDocument(10, ['storage', 'storage', 'storage']);
  builder.addDocument(11, ['storage']);
  builder.addDocument(5000, ['storage', 'storage']);
  const segment = new Segment(builder.build());

  const it = segment.lookup('storage')!;
  assert.equal(it.docCount, 3);
  assert.equal(it.next(), 10);
  assert.equal(it.freq(), 3);
  assert.equal(it.next(), 11);
  assert.equal(it.freq(), 1);
  assert.equal(it.next(), 5000);
  assert.equal(it.freq(), 2);
});

test("'database AND storage' matches brute-force set intersection", () => {
  const { segment, databaseDocs, storageDocs } = buildSkewedCorpus();
  const expected = [...databaseDocs]
    .filter((id) => storageDocs.has(id))
    .sort((a, b) => a - b);

  const { docIds } = segment.intersect(['database', 'storage']);
  assert.deepEqual(docIds, expected);
  assert.ok(docIds.length > 0);
});

test('skip pointers leap over the long chain driven by the short one', () => {
  const { segment } = buildSkewedCorpus();
  const { docIds, stats } = segment.intersect(['database', 'storage']);

  const database = stats.find((s) => s.term === 'database')!;
  const storage = stats.find((s) => s.term === 'storage')!;

  // The short chain drives the merge.
  assert.equal(database.docCount, 12);
  assert.equal(storage.docCount, 3000);

  // Skip pointers were actually used on the long chain...
  assert.ok(storage.skipsUsed > 0, 'expected skip jumps on the long chain');
  // ...and only a tiny fraction of its 3000 postings was decoded:
  // at most ~12 jumps * (SKIP_INTERVAL - 1) sequential decodes plus landings.
  assert.ok(
    storage.decoded <= docIds.length + 12 * SKIP_INTERVAL,
    `decoded ${storage.decoded} postings, expected skip-driven evaluation`,
  );
  assert.ok(
    storage.decoded < storage.docCount / 10,
    `decoded ${storage.decoded}/${storage.docCount}: skipping not effective`,
  );
});

test('advance() lands exactly on skip boundaries and past the end', () => {
  const builder = new SegmentBuilder();
  for (let i = 0; i < 100; i++) builder.addDocument(i * 10, ['storage']);
  const segment = new Segment(builder.build());

  const it = segment.lookup('storage')!;
  // docId 160 is the first posting of the 2nd skip block (index 16).
  assert.equal(it.advance(160), 160);
  assert.equal(it.advance(160), 160, 'advance to current doc is a no-op');
  assert.equal(it.advance(161), 170);
  assert.equal(it.advance(99999), Number.MAX_SAFE_INTEGER);
});

test('multi-term AND chains skip-driven intersections', () => {
  const { segment, databaseDocs, storageDocs, indexDocs } = buildSkewedCorpus();
  const expected = [...databaseDocs]
    .filter((id) => storageDocs.has(id) && indexDocs.has(id))
    .sort((a, b) => a - b);

  const { docIds, stats } = segment.intersect(['database', 'storage', 'index']);
  assert.deepEqual(docIds, expected);
  assert.equal(stats[0].term, 'database', 'shortest chain must drive');
});

test('intersection with an unknown term is empty', () => {
  const { segment } = buildSkewedCorpus();
  const { docIds, stats } = segment.intersect(['database', 'nonexistent']);
  assert.deepEqual(docIds, []);
  assert.deepEqual(stats, []);
});

test('segment survives a write/read round-trip through disk', () => {
  const { segment, databaseDocs, storageDocs } = buildSkewedCorpus();
  const dir = mkdtempSync(join(tmpdir(), 'sis1-'));
  const file = join(dir, 'segment.sis1');

  const builder = new SegmentBuilder();
  builder.addDocument(1, ['database', 'storage']);
  writeFileSync(file, builder.build());
  const reloaded = new Segment(readFileSync(file));
  assert.deepEqual(reloaded.intersect(['database', 'storage']).docIds, [1]);

  const expected = [...databaseDocs].filter((id) => storageDocs.has(id)).sort((a, b) => a - b);
  assert.deepEqual(segment.intersect(['storage', 'database']).docIds, expected);
});
