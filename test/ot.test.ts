import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyOp,
  del,
  insert,
  InvalidBaseVersionError,
  InvalidOperationError,
  Replica,
  transform,
  validateOp,
} from '../src/index.ts';

/** Deterministic PRNG (mulberry32) so fuzz runs are reproducible. */
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('concurrent inserts at the same position converge in any delivery order', () => {
  for (const order of ['ab', 'ba'] as const) {
    const a = new Replica(1, 'hello');
    const b = new Replica(2, 'hello');
    const msgA = a.local(insert(5, 'A'));
    const msgB = b.local(insert(5, 'B'));
    if (order === 'ab') {
      a.receive(msgB);
      b.receive(msgA);
    } else {
      b.receive(msgA);
      a.receive(msgB);
    }
    assert.equal(a.text, b.text, `delivery order ${order}`);
  }
  // Stable order: lower site id inserts first.
  const a = new Replica(1, 'hello');
  const b = new Replica(2, 'hello');
  const msgA = a.local(insert(5, 'A'));
  const msgB = b.local(insert(5, 'B'));
  a.receive(msgB);
  b.receive(msgA);
  assert.equal(a.text, 'helloAB');
  assert.equal(b.text, 'helloAB');
});

test('insert tie-break is a total order: equal site ids fall back to text', () => {
  const x = transform(insert(3, 'b'), insert(3, 'a'), 7, 7);
  const y = transform(insert(3, 'a'), insert(3, 'b'), 7, 7);
  assert.deepEqual(x, [insert(4, 'b')]);
  assert.deepEqual(y, [insert(3, 'a')]);
});

test('overlapping deletes do not double-delete uncovered characters', () => {
  // "abcdefghij": site 1 deletes [2,7) "cdefg", site 2 deletes [4,9) "efgh".
  // Intersection "efg" must be deleted exactly once; union "cdefgh" goes away.
  const a = new Replica(1, 'abcdefghij');
  const b = new Replica(2, 'abcdefghij');
  const msgA = a.local(del(2, 5));
  const msgB = b.local(del(4, 5));
  a.receive(msgB);
  b.receive(msgA);
  assert.equal(a.text, 'abj');
  assert.equal(b.text, 'abj');
});

test('delete fully covered by another delete transforms to nothing', () => {
  assert.deepEqual(transform(del(3, 2), del(0, 10), 1, 2), []);
  const a = new Replica(1, 'abcdefghij');
  const b = new Replica(2, 'abcdefghij');
  const msgA = a.local(del(0, 10));
  const msgB = b.local(del(3, 2));
  a.receive(msgB);
  b.receive(msgA);
  assert.equal(a.text, '');
  assert.equal(b.text, '');
});

test('delete is split by a concurrent insert inside its range', () => {
  const ops = transform(del(2, 6), insert(4, 'XY'), 1, 2);
  // Segments apply in order, so the second is in post-first-segment coordinates.
  assert.deepEqual(ops, [del(2, 2), del(4, 4)]);
  const a = new Replica(1, 'aaaaaaaa'); // 8 x 'a'
  const b = new Replica(2, 'aaaaaaaa');
  const msgA = a.local(del(2, 6));
  const msgB = b.local(insert(4, 'XY'));
  a.receive(msgB);
  b.receive(msgA);
  // 6 of the 8 a's are deleted, the concurrent 'XY' survives.
  assert.equal(a.text, 'aaXY');
  assert.equal(b.text, 'aaXY');
});

test('insert inside a deleted range clamps to the range start', () => {
  assert.deepEqual(transform(insert(5, 'X'), del(3, 4), 1, 2), [insert(3, 'X')]);
  const a = new Replica(1, '0123456789');
  const b = new Replica(2, '0123456789');
  const msgA = a.local(insert(5, 'X'));
  const msgB = b.local(del(3, 4));
  a.receive(msgB);
  b.receive(msgA);
  assert.equal(a.text, b.text);
  assert.equal(a.text, '012X789');
});

test('surrogate pairs: offsets are UTF-16 code units and pairs stay intact', () => {
  const doc = 'a\u{1F600}b'; // 'a', emoji (2 units), 'b' -> length 4
  assert.equal(doc.length, 4);
  const a = new Replica(1, doc);
  const b = new Replica(2, doc);
  // Insert between the emoji and 'b' (offset 3), delete 'a' (offset 0).
  const msgA = a.local(insert(3, 'Z'));
  const msgB = b.local(del(0, 1));
  a.receive(msgB);
  b.receive(msgA);
  assert.equal(a.text, '\u{1F600}Zb');
  assert.equal(a.text, b.text);
  assert.ok(a.text.isWellFormed());
});

test('operations that would split a surrogate pair are rejected', () => {
  const doc = 'x\u{1F600}y'; // emoji occupies offsets 1..3
  assert.throws(() => validateOp(doc, insert(2, 'Q')), InvalidOperationError);
  assert.throws(() => validateOp(doc, del(0, 2)), InvalidOperationError);
  assert.throws(() => validateOp(doc, del(2, 2)), InvalidOperationError);
  assert.throws(() => validateOp(doc, insert(0, '\uD83D')), InvalidOperationError);
  // Deleting the whole pair is fine.
  assert.doesNotThrow(() => validateOp(doc, del(1, 2)));
  const r = new Replica(1, doc);
  assert.throws(() => r.local(del(0, 2)), InvalidOperationError);
});

test('LF and CRLF line endings are plain UTF-16 code units', () => {
  for (const eol of ['\n', '\r\n']) {
    const doc = `ab${eol}cd${eol}ef`;
    const a = new Replica(1, doc);
    const b = new Replica(2, doc);
    // Insert at start of second line, delete first line concurrently.
    const msgA = a.local(insert(2 + eol.length, '>>'));
    const msgB = b.local(del(0, 2 + eol.length));
    a.receive(msgB);
    b.receive(msgA);
    assert.equal(a.text, `>>cd${eol}ef`);
    assert.equal(a.text, b.text);
  }
});

test('invalid base versions are rejected', () => {
  const r = new Replica(1, 'abc');
  const msg = r.local(insert(0, 'X')); // version now 1
  assert.throws(() => r.receive({ baseVersion: -1, siteId: 2, op: msg.op }), InvalidBaseVersionError);
  assert.throws(() => r.receive({ baseVersion: 2, siteId: 2, op: msg.op }), InvalidBaseVersionError);
  assert.throws(() => r.receive({ baseVersion: 0.5, siteId: 2, op: msg.op }), InvalidBaseVersionError);
  assert.throws(() => r.receive({ baseVersion: Number.NaN, siteId: 2, op: msg.op }), InvalidBaseVersionError);
  // baseVersion 0 and 1 are both fine.
  assert.doesNotThrow(() => r.receive({ baseVersion: 0, siteId: 2, op: insert(0, 'Y') }));
  assert.doesNotThrow(() => r.receive({ baseVersion: r.version, siteId: 2, op: insert(0, 'Z') }));
});

test('out-of-range ops are rejected', () => {
  const r = new Replica(1, 'abc');
  assert.throws(() => r.local(insert(4, 'X')), InvalidOperationError);
  assert.throws(() => r.local(insert(-1, 'X')), InvalidOperationError);
  assert.throws(() => r.local(del(2, 5)), InvalidOperationError);
  assert.throws(() => r.local(del(0, -1)), InvalidOperationError);
});

test('transformed deletes always have valid lengths and positions', () => {
  // Exhaustive: every delete/delete pair over a length-6 doc, ranges 0..6.
  for (let p1 = 0; p1 <= 6; p1++) {
    for (let l1 = 0; l1 <= 6 - p1; l1++) {
      for (let p2 = 0; p2 <= 6; p2++) {
        for (let l2 = 0; l2 <= 6 - p2; l2++) {
          for (const op of transform(del(p1, l1), del(p2, l2), 1, 2)) {
            assert.ok(op.length >= 0, `negative length for ${p1},${l1} vs ${p2},${l2}`);
            assert.ok(op.pos >= 0, `negative pos for ${p1},${l1} vs ${p2},${l2}`);
            assert.ok(op.pos + op.length <= 6 - l2, 'result exceeds post-delete doc');
          }
        }
      }
    }
  }
});

test('fuzz: concurrent ops from the same base version converge (TP1)', () => {
  const alphabet = ['a', 'b', 'c', '\n', '\r\n', '\u{1F600}', '\u{1F389}', 'xy'];
  const rand = rng(20260924);
  const pick = <T,>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)];

  const randomDoc = (): string => {
    const parts: string[] = [];
    const n = Math.floor(rand() * 12);
    for (let i = 0; i < n; i++) parts.push(pick(alphabet));
    return parts.join('');
  };

  // All valid surrogate-safe boundaries of a doc.
  const boundaries = (doc: string): number[] => {
    const out: number[] = [];
    for (let i = 0; i <= doc.length; i++) {
      const code = doc.charCodeAt(i);
      if (i > 0 && i < doc.length && code >= 0xdc00 && code <= 0xdfff) continue;
      out.push(i);
    }
    return out;
  };

  const randomOp = (doc: string) => {
    const b = boundaries(doc);
    if (rand() < 0.5 || b.length < 2) {
      const pos = pick(b);
      const len = 1 + Math.floor(rand() * 3);
      return insert(pos, Array.from({ length: len }, () => pick(alphabet)).join(''));
    }
    const pos = pick(b.slice(0, -1));
    const after = b.filter((x) => x > pos);
    const end = pick(after);
    return del(pos, end - pos);
  };

  for (let round = 0; round < 1000; round++) {
    const doc = randomDoc();
    // Two sites, one concurrent op each, both against the same base version.
    for (const [idA, idB] of [[1, 2], [2, 1]]) {
      const a = new Replica(idA, doc);
      const b = new Replica(idB, doc);
      const msgA = a.local(randomOp(doc));
      const msgB = b.local(randomOp(doc));
      // Deliver in both orders across the two iterations of the pair.
      if (idA < idB) {
        a.receive(msgB);
        b.receive(msgA);
      } else {
        b.receive(msgA);
        a.receive(msgB);
      }
      assert.equal(a.text, b.text, `diverged in round ${round} (sites ${idA},${idB})`);
      assert.ok(a.text.isWellFormed(), `lone surrogate in round ${round}`);
      assert.equal(a.version, b.version);
    }
  }
});

test('fuzz: serialized chains always converge', () => {
  const rand = rng(42);
  const alphabet = ['a', 'b', '\n', '\r\n', '\u{1F600}'];
  const pick = <T,>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)];
  const a = new Replica(1, 'start\r\n');
  const b = new Replica(2, 'start\r\n');
  for (let round = 0; round < 200; round++) {
    // Whoever edits does so on the latest converged state, then ships the op.
    const editor = rand() < 0.5 ? a : b;
    const other = editor === a ? b : a;
    const doc = editor.text;
    // Surrogate-safe boundaries only.
    const bounds: number[] = [];
    for (let i = 0; i <= doc.length; i++) {
      const c = doc.charCodeAt(i);
      if (i > 0 && i < doc.length && c >= 0xdc00 && c <= 0xdfff) continue;
      bounds.push(i);
    }
    const op = rand() < 0.5 || bounds.length < 2
      ? insert(pick(bounds), pick(alphabet))
      : del(pick(bounds.slice(0, -1)), 0); // placeholder, replaced below
    if (op.kind === 'delete') {
      const start = op.pos;
      const ends = bounds.filter((x) => x > start);
      op.length = pick(ends) - start;
    }
    const msg = editor.local(op);
    other.receive(msg);
    assert.equal(a.text, b.text, `diverged in round ${round}`);
    assert.ok(a.text.isWellFormed());
  }
});

test('three sites converge with chained transforms', () => {
  const doc = 'The quick\r\nbrown fox\njumps \u{1F980} over';
  const sites = [new Replica(1, doc), new Replica(2, doc), new Replica(3, doc)];
  const m1 = sites[0].local(insert(4, 'very '));
  const m2 = sites[1].local(del(11, 6)); // delete "brown "
  const m3 = sites[2].local(insert(doc.length, '!'));
  const m4 = sites[0].local(del(0, 4)); // based on site-1 state after m1
  for (const m of [m1, m2, m3, m4]) {
    for (const s of sites) {
      if (s.siteId !== m.siteId) s.receive(m);
    }
  }
  assert.equal(sites[0].text, sites[1].text);
  assert.equal(sites[1].text, sites[2].text);
  assert.equal(sites[0].text, 'very quick\r\nfox\njumps \u{1F980} over!');
});

test('applyOp is a pure string splice', () => {
  assert.equal(applyOp('abcd', insert(2, 'XY')), 'abXYcd');
  assert.equal(applyOp('abcd', del(1, 2)), 'ad');
});
