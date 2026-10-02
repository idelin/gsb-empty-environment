import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DeltaDocument, ROOT_ID, type Op } from '../src/document.ts';

function docWithText(replica: string, text: string): DeltaDocument {
  const doc = new DeltaDocument(replica);
  for (const ch of text) doc.append(ch);
  return doc;
}

test('local inserts and deletes produce the expected text', () => {
  const doc = docWithText('A', 'hello');
  assert.equal(doc.text(), 'hello');
  doc.insertAt(0, '>');
  assert.equal(doc.text(), '>hello');
  doc.insertAt(6, '<');
  assert.equal(doc.text(), '>hello<');
  doc.deleteAt(0);
  assert.equal(doc.text(), 'hello<');
  doc.deleteAt(5);
  assert.equal(doc.text(), 'hello');
  assert.equal(doc.length, 5);
});

test('version vector tracks contiguous per-replica sequences', () => {
  const doc = docWithText('A', 'abc');
  doc.deleteAt(1);
  assert.deepEqual(doc.version(), { A: 4 });
});

test('concurrent inserts converge to identical state and text', () => {
  const a = docWithText('A', 'ab');
  const b = new DeltaDocument('B');
  b.merge(a.serializeState());

  // Concurrent: A inserts after 'a', B inserts after 'a' too.
  a.insertAt(1, 'X');
  b.insertAt(1, 'Y');

  const aDelta = a.deltaSince(b.version());
  const bDelta = b.deltaSince(a.version());
  a.merge(bDelta);
  b.merge(aDelta);

  assert.equal(a.fingerprint(), b.fingerprint());
  assert.equal(a.text(), b.text());
  // Children of 'a' ordered by (seq, replica) desc: X(A:3), b(A:2), Y(B:1).
  assert.equal(a.text(), 'aXbY');
});

test('merge is idempotent: re-delivering the same delta changes nothing', () => {
  const a = docWithText('A', 'hello');
  const b = new DeltaDocument('B');
  const state = a.serializeState();
  assert.equal(b.merge(state), 5);
  assert.equal(b.merge(state), 0);
  assert.equal(b.merge(state), 0);
  assert.equal(b.text(), 'hello');
  assert.equal(b.fingerprint(), a.fingerprint());
});

test('out-of-order ops are buffered until their predecessor arrives', () => {
  const a = docWithText('A', 'abc');
  const [op1, op2, op3] = a.serializeState();
  const b = new DeltaDocument('B');
  assert.equal(b.applyOp(op3), false); // gap: seq 3 before 1..2
  assert.equal(b.applyOp(op1), true);
  assert.equal(b.applyOp(op2), true); // drains the buffer
  assert.equal(b.text(), 'abc');
  assert.deepEqual(b.version(), { A: 3 });
});

test('cross-replica orphan insert resolves when its parent arrives', () => {
  const a = docWithText('A', 'x');
  const b = new DeltaDocument('B');
  b.merge(a.serializeState());
  b.insertAt(1, 'y'); // child of A:1

  const bOps = b.deltaSince(a.version()); // B's insert op only
  const c = new DeltaDocument('C');
  c.merge(bOps); // child arrives before its parent A:1
  assert.equal(c.text(), ''); // parked as orphan
  c.merge(a.serializeState());
  assert.equal(c.text(), 'xy');
  assert.equal(c.fingerprint(), b.fingerprint());
});

test('tombstone arriving before its target insert still deletes it', () => {
  const a = docWithText('A', 'xy');
  const b = new DeltaDocument('B');
  b.merge(a.serializeState());
  b.deleteAt(0); // deletes A:1

  const c = new DeltaDocument('C');
  c.merge(b.deltaSince(a.version())); // delete first
  c.merge(a.serializeState()); // then the inserts
  assert.equal(c.text(), 'y');
  assert.equal(c.fingerprint(), b.fingerprint());
});

test('deltaSince returns exactly the ops above the watermark', () => {
  const a = docWithText('A', 'hello');
  const before = a.version();
  a.insertAt(5, '!');
  a.deleteAt(0);
  const delta = a.deltaSince(before);
  assert.equal(delta.length, 2);
  assert.deepEqual(delta.map((op: Op) => op.s), [6, 7]);
  assert.equal(a.deltaSince(a.version()).length, 0);
});

test('insert at head uses the root sentinel', () => {
  const doc = docWithText('A', 'ab');
  const op = doc.insertAt(0, 'z');
  assert.equal(op.p, ROOT_ID);
  assert.equal(doc.text(), 'zab');
});
