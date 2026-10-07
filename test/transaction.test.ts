import { beginTransaction, replaceAll, TextDocument, TransactionSuccess } from '../src';
import { assert, describe, it } from './helpers';

function replaceOp(doc: TextDocument, re: RegExp, rep: string) {
  return { document: doc, expectVersion: doc.version, edits: replaceAll(doc.text, re, rep).edits };
}

describe('multi-document transactions', () => {
  it('commits edits to every document at the frozen version', () => {
    const a = new TextDocument('a.txt', 'foo bar');
    const b = new TextDocument('b.txt', 'baz qux');
    const tx = beginTransaction([replaceOp(a, /bar/, 'BAR'), replaceOp(b, /baz/, 'BAZ')]);
    const result = tx.commit();
    assert.ok(result.ok);
    assert.strictEqual(a.text, 'foo BAR');
    assert.strictEqual(b.text, 'BAZ qux');
    assert.strictEqual(a.version, 1);
    assert.strictEqual(b.version, 1);
    assert.strictEqual(result.changes.length, 2);
  });

  it('returns reversible edits and range mappings on success', () => {
    const a = new TextDocument('a.txt', 'foo bar');
    const tx = beginTransaction([replaceOp(a, /bar/, 'LONGER')]);
    const result = tx.commit();
    assert.ok(result.ok);
    const change = result.changes[0];
    assert.deepStrictEqual(change.edits, [{ start: 4, end: 7, newText: 'LONGER' }]);
    assert.deepStrictEqual(change.inverse, [{ start: 4, end: 10, oldText: 'bar' }]);
    assert.deepStrictEqual(change.map.oldRangeToNew({ start: 4, end: 7 }), { start: 4, end: 10 });
    assert.strictEqual(change.fromVersion, 0);
    assert.strictEqual(change.toVersion, 1);
  });

  it('undo restores every document text and version', () => {
    const a = new TextDocument('a.txt', 'foo bar', 5);
    const b = new TextDocument('b.txt', 'baz qux', 7);
    const tx = beginTransaction([replaceOp(a, /bar/, 'BAR'), replaceOp(b, /baz/, 'BAZ')]);
    const result = tx.commit();
    assert.ok(result.ok);
    result.undo();
    assert.strictEqual(a.text, 'foo bar');
    assert.strictEqual(a.version, 5);
    assert.strictEqual(b.text, 'baz qux');
    assert.strictEqual(b.version, 7);
  });

  it('rejects the whole batch when any document version changed', () => {
    const a = new TextDocument('a.txt', 'foo bar');
    const b = new TextDocument('b.txt', 'baz qux');
    const tx = beginTransaction([replaceOp(a, /bar/, 'BAR'), replaceOp(b, /baz/, 'BAZ')]);
    // Concurrent modification to b after the transaction was prepared.
    b.applyEdits([{ start: 4, end: 7, newText: 'QUX' }]);
    const result = tx.commit();
    assert.ok(!result.ok);
    assert.strictEqual(result.conflicts.length, 1);
    assert.strictEqual(result.conflicts[0].document, b);
    assert.strictEqual(result.conflicts[0].expectVersion, 0);
    assert.strictEqual(result.conflicts[0].actualVersion, 1);
    // Atomic rejection: neither document took the transaction's edits.
    assert.strictEqual(a.text, 'foo bar');
    assert.strictEqual(a.version, 0);
    assert.strictEqual(b.text, 'baz QUX');
    assert.strictEqual(b.version, 1);
  });

  it('rejects a stale expected version even without other changes', () => {
    const a = new TextDocument('a.txt', 'foo bar', 3);
    const tx = beginTransaction([
      { document: a, expectVersion: 2, edits: [{ start: 4, end: 7, newText: 'BAR' }] },
    ]);
    const result = tx.commit();
    assert.ok(!result.ok);
    assert.strictEqual(a.text, 'foo bar');
    assert.strictEqual(a.version, 3);
  });

  it('cannot be committed twice', () => {
    const a = new TextDocument('a.txt', 'foo bar');
    const tx = beginTransaction([replaceOp(a, /bar/, 'BAR')]);
    assert.ok(tx.commit().ok);
    assert.throws(() => tx.commit(), /already committed/);
  });
});

describe('apply-then-undo round-trips', () => {
  function roundTrip(name: string, text: string, re: RegExp, rep: string): void {
    it(name, () => {
      const doc = new TextDocument('doc.txt', text);
      const expected = replaceAll(text, re, rep);
      const tx = beginTransaction([{ document: doc, expectVersion: doc.version, edits: expected.edits }]);
      const result = tx.commit();
      assert.ok(result.ok);
      assert.strictEqual(doc.text, expected.text);
      assert.strictEqual(doc.version, 1);
      result.undo();
      assert.strictEqual(doc.text, text);
      assert.strictEqual(doc.version, 0);
    });
  }

  roundTrip('zero-width insertions at every boundary', 'ab', /(?:)/, '-');
  roundTrip('zero-width lookahead matches', 'aa', /(?=a)/, '-');
  roundTrip('non-participating capture expands empty', 'b b', /(a)|(b)/, '<$1>');
  roundTrip('adjacent matches with empty replacement', 'abab', /ab/, '');
  roundTrip('capture swap with named groups', 'John Smith', /(?<f>\w+) (?<l>\w+)/, '$<l> $<f>');

  it('undo after concurrent modification was rejected leaves docs untouched', () => {
    const a = new TextDocument('a.txt', 'foo bar');
    const b = new TextDocument('b.txt', 'baz qux');
    const tx = beginTransaction([replaceOp(a, /bar/, 'BAR'), replaceOp(b, /baz/, 'BAZ')]);
    b.applyEdits([{ start: 0, end: 3, newText: 'BAZ' }]);
    const result = tx.commit();
    assert.ok(!result.ok);
    assert.strictEqual(a.text, 'foo bar');
    assert.strictEqual(b.text, 'BAZ qux');
  });

  it('undo restores after a zero-width-only transaction', () => {
    const doc = new TextDocument('doc.txt', 'ab');
    const edits = replaceAll(doc.text, /(?:)/, '-').edits;
    const tx = beginTransaction([{ document: doc, expectVersion: 0, edits }]);
    const result = tx.commit() as TransactionSuccess;
    assert.ok(result.ok);
    assert.strictEqual(doc.text, '-a-b-');
    result.undo();
    assert.strictEqual(doc.text, 'ab');
    assert.strictEqual(doc.version, 0);
  });
});
