/**
 * Demo: build a compact read-only segment where the posting lists for
 * 'database' (short chain) and 'storage' (long chain) are heavily skewed,
 * then evaluate `database AND storage` with skip-pointer intersection.
 *
 * Run: node examples/demo.ts   (Node >= 22.6 with type stripping)
 */
import { Segment, SegmentBuilder } from '../src/index.ts';

const STORAGE_DOCS = 100_000;
const DATABASE_DOCS = [7, 512, 1_024, 5_000, 12_345, 40_000, 77_777, 90_001, 99_999, 150_000];

console.log(`Building segment: 'storage' in ${STORAGE_DOCS} docs, 'database' in ${DATABASE_DOCS.length} docs...`);
const buildStart = performance.now();

const builder = new SegmentBuilder();
for (let docId = 1; docId <= STORAGE_DOCS; docId++) {
  builder.addDocument(docId, ['storage', 'block', 'device', `topic${docId % 200}`]);
}
for (const docId of DATABASE_DOCS) {
  // Some database docs also mention storage; one (150000) does not.
  const tokens = ['database', 'database', 'query', 'optimizer'];
  if (docId <= STORAGE_DOCS) tokens.push('storage');
  builder.addDocument(docId, tokens);
}

const buffer = builder.build();
const segment = new Segment(buffer);
console.log(
  `Built in ${(performance.now() - buildStart).toFixed(1)} ms — ` +
    `${builder.docCount} docs, ${segment.termCount} terms, segment size ${buffer.length} bytes`,
);

const queryStart = performance.now();
const { docIds, stats } = segment.intersect(['database', 'storage']);
const queryMs = performance.now() - queryStart;

console.log(`\nQuery: database AND storage`);
console.log(`Result (${docIds.length} docs): [${docIds.join(', ')}]`);
console.log(`Evaluated in ${queryMs.toFixed(3)} ms\n`);

console.log('Per-term evaluation stats (shortest chain drives):');
for (const s of stats) {
  console.log(
    `  ${s.term.padEnd(10)} chain length ${String(s.docCount).padStart(7)} | ` +
      `postings decoded ${String(s.decoded).padStart(5)} | skip jumps ${s.skipsUsed}`,
  );
}

const storage = stats.find((s) => s.term === 'storage')!;
console.log(
  `\nSkip pointers let the long 'storage' chain skip ${storage.docCount - storage.decoded} ` +
    `of ${storage.docCount} postings ` +
    `(${((1 - storage.decoded / storage.docCount) * 100).toFixed(2)}% never decoded).`,
);
