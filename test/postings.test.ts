import test from "node:test";
import assert from "node:assert/strict";
import {
  encodePostings,
  PostingsIterator,
  type Posting,
} from "../src/postings.ts";

function makePostings(docIds: number[]): Posting[] {
  return docIds.map((docId, i) => ({ docId, tf: (i % 5) + 1 }));
}

function collectAll(it: PostingsIterator): Posting[] {
  const out: Posting[] = [];
  while (!it.done()) {
    out.push({ docId: it.docId(), tf: it.tf() });
    it.next();
  }
  return out;
}

test("sequential iteration decodes docIds and term frequencies", () => {
  const postings = makePostings([0, 1, 3, 7, 100, 101, 5000]);
  const it = new PostingsIterator(encodePostings(postings, 2), 0);
  assert.equal(it.docFreq, postings.length);
  assert.deepEqual(collectAll(it), postings);
});

test("advance(target) lands on the first docId >= target", () => {
  const docIds = Array.from({ length: 1000 }, (_, i) => i * 3); // 0, 3, 6, ...
  const postings = makePostings(docIds);
  const encoded = encodePostings(postings, 16);

  for (const target of [0, 1, 2, 3, 4, 2997, 2998, 2999, 5000]) {
    const it = new PostingsIterator(encoded, 0);
    it.advance(target);
    const expected = docIds.find((d) => d >= target);
    if (expected === undefined) {
      assert.ok(it.done(), `target=${target}`);
    } else {
      assert.equal(it.docId(), expected, `target=${target}`);
    }
  }
});

test("advance matches a linear scan for every target", () => {
  // Sparse, irregular docIds to exercise skip boundaries.
  const docIds: number[] = [];
  let d = 0;
  let seed = 7;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
  for (let i = 0; i < 2000; i++) {
    d += 1 + Math.floor(rand() * 20);
    docIds.push(d);
  }
  const encoded = encodePostings(makePostings(docIds), 16);

  for (let target = 0; target <= d + 10; target += 37) {
    const it = new PostingsIterator(encoded, 0);
    it.advance(target);
    const expected = docIds.find((x) => x >= target);
    if (expected === undefined) assert.ok(it.done());
    else assert.equal(it.docId(), expected, `target=${target}`);
  }
});

test("advance uses skip pointers instead of decoding everything", () => {
  const docIds = Array.from({ length: 4096 }, (_, i) => i);
  const encoded = encodePostings(makePostings(docIds), 64);
  const it = new PostingsIterator(encoded, 0);
  it.advance(4000);
  assert.equal(it.docId(), 4000);
  assert.ok(it.skipJumps >= 1, "expected at least one skip jump");
  assert.ok(
    it.decodedCount < 200,
    `expected few decodes thanks to skips, got ${it.decodedCount}`,
  );
});

test("mixed next/advance iteration stays consistent", () => {
  const docIds = Array.from({ length: 500 }, (_, i) => i * 2);
  const encoded = encodePostings(makePostings(docIds), 8);
  const it = new PostingsIterator(encoded, 0);
  const seen: number[] = [];
  seen.push(it.docId()); // 0
  it.advance(100);
  seen.push(it.docId()); // 100
  it.next();
  seen.push(it.docId()); // 102
  it.advance(101); // no-op: already past
  seen.push(it.docId()); // 102
  it.advance(998);
  seen.push(it.docId()); // 998
  it.advance(999);
  assert.ok(it.done());
  assert.deepEqual(seen, [0, 100, 102, 102, 998]);
});

test("empty and single-posting lists", () => {
  const empty = new PostingsIterator(encodePostings([], 4), 0);
  assert.ok(empty.done());

  const single = new PostingsIterator(encodePostings([{ docId: 42, tf: 3 }], 4), 0);
  assert.equal(single.docId(), 42);
  assert.equal(single.tf(), 3);
  single.next();
  assert.ok(single.done());
});
