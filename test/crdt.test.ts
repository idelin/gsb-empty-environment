import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TextDoc } from '../src/index.ts';
import type { InsertOp, DeleteOp, Snapshot } from '../src/index.ts';

function snapEq(a: Snapshot, b: Snapshot): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

test('basic insert and delete materialize visible text', () => {
  const doc = new TextDoc('A');
  doc.insertAt(0, 'hello');
  assert.equal(doc.getText(), 'hello');
  assert.equal(doc.length, 5);
  doc.deleteAt(1, 3); // remove "ell"
  assert.equal(doc.getText(), 'ho');
});

test('insert with missing causal dependency is buffered, not attached to head', () => {
  const a = new TextDoc('A');
  const ops = a.insertAt(0, 'ac');
  // Build an op that depends on the second element ("c") only.
  const b = new TextDoc('B');
  for (const op of ops) b.apply(op); // b knows "ac"
  const tail = b.insertAt(2, '!')[0]; // parented on "c"

  const c = new TextDoc('C');
  c.apply(tail); // parent "c" unknown to C
  assert.equal(c.getText(), '', 'buffered op must not become visible');
  assert.equal(c.length, 0, 'buffered op must not be re-anchored at the head');

  c.apply(ops[0]); // "a" arrives, still missing "c"
  assert.equal(c.getText(), 'a');
  c.apply(ops[1]); // "c" arrives, dependency satisfied
  assert.equal(c.getText(), 'ac!', 'buffered op lands exactly after its parent');
});

test('deletes are idempotent and kept as tombstones', () => {
  const a = new TextDoc('A');
  const ops = a.insertAt(0, 'ab');
  const del: DeleteOp = { type: 'delete', id: ops[0].id };
  a.apply(del);
  assert.equal(a.getText(), 'b');
  const before = a.snapshot();
  a.apply(del);
  a.apply(del);
  assert.ok(snapEq(a.snapshot(), before), 'repeated delete changes nothing');
  assert.equal(a.getText(), 'b');
  // Tombstone still occupies a slot: an insert anchored on the deleted
  // element still resolves its causal dependency.
  const child: InsertOp = { type: 'insert', id: { replica: 'B', counter: 1 }, parent: ops[0].id, value: 'x' };
  a.apply(child);
  // Siblings of tombstoned "a" sort by id descending: b(A:2) before x(B:1).
  assert.equal(a.getText(), 'bx');
});

test('delete arriving before its insert keeps the char invisible', () => {
  const a = new TextDoc('A');
  const ops = a.insertAt(0, 'z');
  const del: DeleteOp = { type: 'delete', id: ops[0].id };

  const b = new TextDoc('B');
  b.apply(del); // delete first
  b.apply(ops[0]); // insert afterwards
  assert.equal(b.getText(), '', 'char stays invisible');
  assert.equal(b.length, 0);
  // And it converges with a replica that saw them in order.
  const c = new TextDoc('C');
  c.apply(ops[0]);
  c.apply(del);
  assert.equal(c.getText(), b.getText());
  assert.ok(snapEq(b.snapshot(), c.snapshot()));
});

test('duplicate delivery of the same message changes nothing', () => {
  const a = new TextDoc('A');
  const inserts = a.insertAt(0, 'hey');
  const dels = a.deleteAt(0, 1);

  const b = new TextDoc('B');
  for (const op of [...inserts, ...dels]) b.apply(op);
  const before = b.snapshot();
  for (let round = 0; round < 3; round++) {
    for (const op of [...inserts, ...dels]) b.apply(op);
  }
  assert.ok(snapEq(b.snapshot(), before));
  assert.equal(b.getText(), 'ey');
});

test('merge is idempotent', () => {
  const a = new TextDoc('A');
  const b = new TextDoc('B');
  a.insertAt(0, 'foo');
  b.insertAt(0, 'bar');
  const m = TextDoc.merged(a, b);
  const once = m.snapshot();
  m.merge(b);
  m.merge(a);
  assert.ok(snapEq(m.snapshot(), once));
});

test('merge is commutative', () => {
  const a = new TextDoc('A');
  const b = new TextDoc('B');
  a.insertAt(0, 'ab');
  b.insertAt(0, 'xy');
  a.deleteAt(0, 1);
  const ab = TextDoc.merged(a, b);
  const ba = TextDoc.merged(b, a);
  assert.equal(ab.getText(), ba.getText());
  assert.ok(snapEq(ab.snapshot(), ba.snapshot()));
});

test('merge is associative', () => {
  const a = new TextDoc('A');
  const b = new TextDoc('B');
  const c = new TextDoc('C');
  a.insertAt(0, '1');
  b.insertAt(0, '22');
  c.insertAt(0, '333');
  b.deleteAt(0, 1);
  const abC = TextDoc.merged(TextDoc.merged(a, b), c);
  const aBc = TextDoc.merged(a, TextDoc.merged(b, c));
  assert.equal(abC.getText(), aBc.getText());
  assert.ok(snapEq(abC.snapshot(), aBc.snapshot()));
});

test('concurrent replicas converge after exchanging ops in different orders', () => {
  const a = new TextDoc('A');
  const b = new TextDoc('B');
  const seed = a.insertAt(0, 'hi');
  for (const op of seed) b.apply(op);

  const opsA = a.insertAt(2, '!'); // A appends
  const opsB = b.insertAt(0, '> '); // B prepends (2 code points)
  const delB = b.deleteAt(2, 1); // B deletes 'h' (text is "> hi")

  const applyAll = (d: TextDoc, ops: readonly { type: string }[]): void => {
    for (const op of ops) d.apply(op as never);
  };
  applyAll(a, [...opsB, ...delB]); // A receives in order
  applyAll(b, [...opsA].reverse()); // B receives in reverse order
  assert.equal(a.getText(), b.getText());
  assert.equal(a.getText(), '> i!');
});

test('gc collects tombstones only when every replica acks the watermark', () => {
  const a = new TextDoc('A');
  const b = new TextDoc('B');
  const ops = a.insertAt(0, 'xy');
  for (const op of ops) b.apply(op);
  const dels = a.deleteAt(0, 2);
  for (const op of dels) b.apply(op);
  assert.equal(a.getText(), '');
  assert.equal(b.getText(), '');

  // B is part of the system but has acked nothing yet: nothing may be collected.
  const ackOnlyA = new Map([['A', a.watermark()], ['B', new Map()]]);
  assert.deepEqual(a.gc(ackOnlyA), [], 'no collection before all replicas ack');

  // B acks only the first op: still nothing (watermark is a contiguous prefix).
  const ackPartial = new Map([
    ['A', a.watermark()],
    ['B', new Map([['A', 1]])],
  ]);
  assert.deepEqual(a.gc(ackPartial), []);

  // All replicas ack everything: tombstones collected on both sides.
  const ackAll = new Map([
    ['A', a.watermark()],
    ['B', b.watermark()],
  ]);
  const collectedA = a.gc(ackAll);
  const collectedB = b.gc(ackAll);
  assert.equal(collectedA.length, 2);
  assert.equal(collectedB.length, 2);
  assert.equal(a.getText(), '');
  assert.ok(snapEq(a.snapshot(), b.snapshot()), 'replicas stay identical after gc');
});

test('gc never collects a tombstone that still has children', () => {
  const a = new TextDoc('A');
  const ops = a.insertAt(0, 'ab');
  // Child anchored on the first element.
  const child: InsertOp = { type: 'insert', id: { replica: 'B', counter: 1 }, parent: ops[0].id, value: 'c' };
  a.apply(child);
  a.applyDelete({ type: 'delete', id: ops[0].id }); // "a" becomes a tombstone with a child
  // Siblings sort by id descending: b(A:2) precedes c(B:1); "a" is tombstoned.
  assert.equal(a.getText(), 'bc');

  const ackAll = new Map([
    ['A', a.watermark()],
    ['B', new Map([['A', 2], ['B', 1]])],
  ]);
  const collected = a.gc(ackAll);
  assert.deepEqual(collected, [], 'parent tombstone of a live child survives gc');
  assert.equal(a.getText(), 'bc');
});
