import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  BatchValidationError,
  DocumentStore,
  UndoConflictError,
} from '../src/index.ts';
import type { BatchReceipt, DocumentEdit, DocumentSnapshot } from '../src/index.ts';

function snapshots(store: DocumentStore): DocumentSnapshot[] {
  return store.listSnapshots();
}

function capture<T extends Error>(fn: () => void): T {
  try {
    fn();
  } catch (error) {
    return error as T;
  }
  throw new Error('expected function to throw');
}

describe('applyBatch', () => {
  it('publishes edits across multiple documents atomically', () => {
    const store = new DocumentStore();
    store.createDocument('a', 'hello world');
    store.createDocument('b', 'foo bar');

    const receipt = store.applyBatch([
      { docId: 'a', expectedVersion: 0, edits: [{ range: { start: 6, end: 11 }, newText: 'there' }] },
      { docId: 'b', expectedVersion: 0, edits: [{ range: { start: 0, end: 3 }, newText: 'baz' }] },
    ]);

    assert.deepEqual(snapshots(store), [
      { docId: 'a', version: 1, text: 'hello there' },
      { docId: 'b', version: 1, text: 'baz bar' },
    ]);
    assert.equal(receipt.undone, false);
    assert.deepEqual(
      receipt.entries.map((e) => [e.docId, e.beforeVersion, e.afterVersion]),
      [['a', 0, 1], ['b', 0, 1]],
    );
  });

  it('interprets same-document edits in original coordinates', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'hello world');

    // Both ranges refer to the original text, not to shifted positions.
    store.applyBatch([
      {
        docId: 'doc',
        expectedVersion: 0,
        edits: [
          { range: { start: 6, end: 11 }, newText: 'there' },
          { range: { start: 0, end: 5 }, newText: 'hi' },
        ],
      },
    ]);

    assert.equal(store.getSnapshot('doc').text, 'hi there');
  });

  it('accepts adjacent (touching) edits as non-overlapping', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'abcd');

    store.applyBatch([
      {
        docId: 'doc',
        expectedVersion: 0,
        edits: [
          { range: { start: 0, end: 2 }, newText: 'X' },
          { range: { start: 2, end: 4 }, newText: 'Y' },
        ],
      },
    ]);

    assert.equal(store.getSnapshot('doc').text, 'XY');
  });

  it('supports insertion and deletion', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'abc');

    store.applyBatch([
      {
        docId: 'doc',
        expectedVersion: 0,
        edits: [
          { range: { start: 1, end: 1 }, newText: 'x' },
          { range: { start: 2, end: 3 }, newText: '' },
        ],
      },
    ]);

    assert.equal(store.getSnapshot('doc').text, 'axb');
  });

  it('rejects edits sharing a start offset even when one is empty', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'abc');
    const before = snapshots(store);

    const error = capture<BatchValidationError>(() =>
      store.applyBatch([
        {
          docId: 'doc',
          expectedVersion: 0,
          edits: [
            { range: { start: 1, end: 1 }, newText: 'x' },
            { range: { start: 1, end: 2 }, newText: 'y' },
          ],
        },
      ]),
    );
    assert.ok(error instanceof BatchValidationError);
    assert.deepEqual(error.issues.map((issue) => issue.kind), ['overlapping-edits']);
    assert.deepEqual(snapshots(store), before);
  });

  it('rejects a version mismatch and modifies nothing', () => {
    const store = new DocumentStore();
    store.createDocument('a', 'aaa');
    store.createDocument('b', 'bbb');
    const before = snapshots(store);

    const batch: DocumentEdit[] = [
      { docId: 'a', expectedVersion: 0, edits: [{ range: { start: 0, end: 1 }, newText: 'x' }] },
      { docId: 'b', expectedVersion: 7, edits: [{ range: { start: 0, end: 1 }, newText: 'y' }] },
    ];
    const error = capture<BatchValidationError>(() => store.applyBatch(batch));
    assert.ok(error instanceof BatchValidationError);
    assert.deepEqual(
      error.issues.map((issue) => [issue.kind, issue.docId]),
      [['version-mismatch', 'b']],
    );
    assert.deepEqual(snapshots(store), before);
  });

  it('rejects out-of-bounds and malformed ranges and modifies nothing', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'abc');
    const before = snapshots(store);

    const error = capture<BatchValidationError>(() =>
      store.applyBatch([
        {
          docId: 'doc',
          expectedVersion: 0,
          edits: [
            { range: { start: 0, end: 4 }, newText: 'x' },
            { range: { start: 3, end: 2 }, newText: 'y' },
            { range: { start: -1, end: 1 }, newText: 'z' },
          ],
        },
      ]),
    );
    assert.ok(error instanceof BatchValidationError);
    assert.deepEqual(
      error.issues.map((issue) => issue.kind),
      ['range-out-of-bounds', 'invalid-range', 'invalid-range'],
    );
    assert.deepEqual(snapshots(store), before);
  });

  it('rejects overlapping edits in original coordinates and modifies nothing', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'abcdef');
    const before = snapshots(store);

    const error = capture<BatchValidationError>(() =>
      store.applyBatch([
        {
          docId: 'doc',
          expectedVersion: 0,
          edits: [
            { range: { start: 1, end: 4 }, newText: 'x' },
            { range: { start: 3, end: 5 }, newText: 'y' },
          ],
        },
      ]),
    );
    assert.ok(error instanceof BatchValidationError);
    assert.deepEqual(error.issues.map((issue) => issue.kind), ['overlapping-edits']);
    assert.deepEqual(snapshots(store), before);
  });

  it('rejects unknown and duplicate documents and modifies nothing', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'abc');
    const before = snapshots(store);

    const error = capture<BatchValidationError>(() =>
      store.applyBatch([
        { docId: 'ghost', expectedVersion: 0, edits: [] },
        { docId: 'doc', expectedVersion: 0, edits: [] },
        { docId: 'doc', expectedVersion: 0, edits: [] },
      ]),
    );
    assert.ok(error instanceof BatchValidationError);
    assert.deepEqual(
      error.issues.map((issue) => issue.kind),
      ['unknown-document', 'duplicate-document'],
    );
    assert.deepEqual(snapshots(store), before);
  });

  it('validates every document before publishing any of them', () => {
    const store = new DocumentStore();
    store.createDocument('ok', 'good');
    store.createDocument('bad', 'bad');
    const before = snapshots(store);

    // The edit for "ok" is valid; only the entry for "bad" is broken.
    assert.throws(
      () =>
        store.applyBatch([
          { docId: 'ok', expectedVersion: 0, edits: [{ range: { start: 0, end: 4 }, newText: 'great' }] },
          { docId: 'bad', expectedVersion: 0, edits: [{ range: { start: 0, end: 99 }, newText: 'x' }] },
        ]),
      BatchValidationError,
    );

    assert.deepEqual(snapshots(store), before);
  });
});

describe('undo', () => {
  it('restores texts and versions of every document in the batch', () => {
    const store = new DocumentStore();
    store.createDocument('a', 'hello world');
    store.createDocument('b', 'foo');
    const before = snapshots(store);

    const receipt = store.applyBatch([
      { docId: 'a', expectedVersion: 0, edits: [{ range: { start: 6, end: 11 }, newText: 'there' }] },
      { docId: 'b', expectedVersion: 0, edits: [{ range: { start: 3, end: 3 }, newText: ' bar' }] },
    ]);
    store.undo(receipt);

    assert.deepEqual(snapshots(store), before);
    assert.equal(receipt.undone, true);
  });

  it('refuses to overwrite later edits and modifies nothing', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'v0');

    const first = store.applyBatch([
      { docId: 'doc', expectedVersion: 0, edits: [{ range: { start: 0, end: 2 }, newText: 'v1' }] },
    ]);
    store.applyBatch([
      { docId: 'doc', expectedVersion: 1, edits: [{ range: { start: 0, end: 2 }, newText: 'v2' }] },
    ]);
    const before = snapshots(store);

    const error = capture<UndoConflictError>(() => store.undo(first));
    assert.ok(error instanceof UndoConflictError);
    assert.deepEqual(error.conflicts, [
      { docId: 'doc', expectedVersion: 1, actualVersion: 2 },
    ]);
    assert.deepEqual(snapshots(store), before);
    assert.equal(first.undone, false);
  });

  it('is atomic across documents: one conflict blocks all restores', () => {
    const store = new DocumentStore();
    store.createDocument('a', 'a0');
    store.createDocument('b', 'b0');

    const receipt = store.applyBatch([
      { docId: 'a', expectedVersion: 0, edits: [{ range: { start: 1, end: 2 }, newText: '1' }] },
      { docId: 'b', expectedVersion: 0, edits: [{ range: { start: 1, end: 2 }, newText: '1' }] },
    ]);
    // A later batch touches only "b".
    store.applyBatch([
      { docId: 'b', expectedVersion: 1, edits: [{ range: { start: 1, end: 2 }, newText: '2' }] },
    ]);
    const before = snapshots(store);

    assert.throws(() => store.undo(receipt), UndoConflictError);
    // "a" must not have been restored even though its version still matched.
    assert.deepEqual(snapshots(store), before);
  });

  it('allows undo after an unrelated document moved on', () => {
    const store = new DocumentStore();
    store.createDocument('a', 'a0');
    store.createDocument('b', 'b0');

    const receipt = store.applyBatch([
      { docId: 'a', expectedVersion: 0, edits: [{ range: { start: 1, end: 2 }, newText: '1' }] },
    ]);
    store.applyBatch([
      { docId: 'b', expectedVersion: 0, edits: [{ range: { start: 1, end: 2 }, newText: '1' }] },
    ]);

    store.undo(receipt);
    assert.deepEqual(snapshots(store), [
      { docId: 'a', version: 0, text: 'a0' },
      { docId: 'b', version: 1, text: 'b1' },
    ]);
  });

  it('rejects undoing the same receipt twice', () => {
    const store = new DocumentStore();
    store.createDocument('doc', 'x');
    const receipt: BatchReceipt = store.applyBatch([
      { docId: 'doc', expectedVersion: 0, edits: [{ range: { start: 0, end: 1 }, newText: 'y' }] },
    ]);

    store.undo(receipt);
    assert.throws(() => store.undo(receipt), /already been undone/);
  });
});
