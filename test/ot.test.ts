import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { applyOp, applyOps, del, insert, transform } from '../src/ot.ts';
import type { TextOp } from '../src/ot.ts';
import { Session } from '../src/session.ts';

/** TP1 check: applying opA then transformed opB equals opB then transformed opA. */
function convergeBothWays(
  doc: string,
  opA: TextOp,
  siteA: number,
  opB: TextOp,
  siteB: number,
): string {
  const left = applyOps(applyOp(doc, opA), transform(opB, siteB, opA, siteA));
  const right = applyOps(applyOp(doc, opB), transform(opA, siteA, opB, siteB));
  assert.equal(left, right, `diverged: ${JSON.stringify(left)} vs ${JSON.stringify(right)}`);
  return left;
}

describe('insert/insert at the same position', () => {
  it('converges for any delivery order, lower site id first', () => {
    const final = convergeBothWays('abc', insert(1, 'X'), 1, insert(1, 'Y'), 2);
    assert.equal(final, 'aXYbc');
  });

  it('converges at document start and end', () => {
    assert.equal(convergeBothWays('ab', insert(0, 'X'), 2, insert(0, 'Y'), 1), 'YXab');
    assert.equal(convergeBothWays('ab', insert(2, 'X'), 1, insert(2, 'Y'), 2), 'abXY');
  });

  it('three sites, all 6 delivery orders agree', () => {
    const ops = [
      { op: insert(0, 'x'), baseVersion: 0, siteId: 1 },
      { op: insert(0, 'y'), baseVersion: 0, siteId: 2 },
      { op: insert(0, 'z'), baseVersion: 0, siteId: 3 },
    ];
    const results = new Set<string>();
    for (const a of ops) {
      for (const b of ops) {
        for (const c of ops) {
          if (a === b || b === c || a === c) continue;
          const s = new Session('ab');
          s.receive(a);
          s.receive(b);
          s.receive(c);
          results.add(s.doc);
        }
      }
    }
    assert.deepEqual([...results], ['xyzab']);
  });
});

describe('overlapping deletes', () => {
  it('does not delete uncovered characters twice', () => {
    // [1,4) "bcd" vs [3,6) "def" over "abcdef" -> only "a" survives.
    const final = convergeBothWays('abcdef', del(1, 3), 1, del(3, 3), 2);
    assert.equal(final, 'a');
  });

  it('a delete fully covered by another becomes a no-op', () => {
    const final = convergeBothWays('abcdef', del(1, 4), 1, del(2, 2), 2);
    assert.equal(final, 'af');
    assert.deepEqual(transform(del(2, 2), 2, del(1, 4), 1), []);
  });

  it('identical deletes collapse into one', () => {
    const final = convergeBothWays('hello', del(1, 3), 1, del(1, 3), 2);
    assert.equal(final, 'ho');
  });

  it('disjoint deletes both apply', () => {
    const final = convergeBothWays('abcdef', del(0, 1), 1, del(4, 2), 2);
    assert.equal(final, 'bcd');
  });
});

describe('insert vs delete', () => {
  it('insert inside a deleted range is pinned to the range start', () => {
    const final = convergeBothWays('abcdef', insert(3, 'X'), 1, del(1, 4), 2);
    assert.equal(final, 'aXf');
  });

  it('delete spanning an insert splits into two deletes', () => {
    const pieces = transform(del(1, 4), 1, insert(3, 'XY'), 2);
    assert.deepEqual(pieces, [del(5, 2), del(1, 2)]);
    const final = convergeBothWays('abcdef', del(1, 4), 1, insert(3, 'XY'), 2);
    assert.equal(final, 'aXYf');
  });

  it('insert at delete boundaries survives', () => {
    assert.equal(convergeBothWays('abc', insert(1, 'X'), 1, del(1, 1), 2), 'aXc');
    assert.equal(convergeBothWays('abc', insert(2, 'X'), 1, del(1, 1), 2), 'aXc');
  });
});

describe('UTF-16 offsets and surrogate pairs', () => {
  const doc = 'a\uD83D\uDE00b'; // "a😀b": offsets a=0, 😀=1..2, b=3

  it('rejects an insert that would split a surrogate pair', () => {
    assert.throws(() => applyOp(doc, insert(2, 'X')), /surrogate pair/);
  });

  it('rejects a delete range that cuts a surrogate pair', () => {
    assert.throws(() => applyOp(doc, del(1, 1)), /surrogate pair/);
    assert.throws(() => applyOp(doc, del(2, 1)), /surrogate pair/);
  });

  it('allows operations on pair boundaries using UTF-16 offsets', () => {
    assert.equal(applyOp(doc, insert(3, 'X')), 'a\uD83D\uDE00Xb');
    assert.equal(applyOp(doc, del(1, 2)), 'ab');
    assert.equal(applyOp(doc, insert(1, '\uD83C\uDF89')), 'a\uD83C\uDF89\uD83D\uDE00b');
  });

  it('rejects unpaired surrogates in inserted text', () => {
    assert.throws(() => applyOp('ab', insert(1, '\uD83D')), /unpaired high/);
    assert.throws(() => applyOp('ab', insert(1, '\uDE00')), /unpaired low/);
  });

  it('transformed ops keep lengths legal on both ends', () => {
    // Concurrent edits around an emoji; both application orders must apply cleanly.
    const final = convergeBothWays(doc, insert(3, 'XY'), 1, del(0, 3), 2);
    assert.equal(final, 'XYb');
    assert.equal(final.length, 3);
  });
});

describe('LF and CRLF line endings', () => {
  it('handles concurrent edits across LF lines', () => {
    const doc = 'one\ntwo\nthree';
    const final = convergeBothWays(doc, insert(4, '1.5\n'), 1, del(8, 5), 2);
    assert.equal(final, 'one\n1.5\ntwo\n');
  });

  it('handles concurrent edits across CRLF lines', () => {
    const doc = 'one\r\ntwo\r\nthree';
    const final = convergeBothWays(doc, insert(5, '1.5\r\n'), 1, del(10, 5), 2);
    assert.equal(final, 'one\r\n1.5\r\ntwo\r\n');
  });

  it('deletes spanning a CRLF pair converge', () => {
    const doc = 'a\r\nb';
    const final = convergeBothWays(doc, del(1, 2), 1, insert(3, 'X'), 2);
    assert.equal(final, 'aXb');
  });

  it('mixed LF/CRLF document stays consistent', () => {
    const doc = 'a\nb\r\nc';
    const final = convergeBothWays(doc, del(0, 3), 1, insert(5, 'Z'), 2);
    assert.equal(final, '\r\nZc');
  });
});

describe('baseline validation', () => {
  it('rejects negative, fractional, and future baselines', () => {
    const s = new Session('abc');
    s.receive({ op: insert(0, 'X'), baseVersion: 0, siteId: 1 });
    assert.throws(
      () => s.receive({ op: insert(0, 'Y'), baseVersion: -1, siteId: 2 }),
      /negative/,
    );
    assert.throws(
      () => s.receive({ op: insert(0, 'Y'), baseVersion: 0.5, siteId: 2 }),
      /integer/,
    );
    assert.throws(
      () => s.receive({ op: insert(0, 'Y'), baseVersion: 2, siteId: 2 }),
      /ahead of current version 1/,
    );
  });

  it('accepts a stale-but-valid baseline and transforms forward', () => {
    const s = new Session('abc');
    s.receive({ op: insert(0, 'X'), baseVersion: 0, siteId: 1 }); // v0 -> v1
    // Still based on v0: must be transformed past the insert of "X".
    const out = s.receive({ op: del(0, 1), baseVersion: 0, siteId: 2 });
    assert.deepEqual(out, [del(1, 1)]);
    assert.equal(s.doc, 'Xbc');
    assert.equal(s.version, 2);
  });
});

describe('session integration', () => {
  it('two sites converge regardless of delivery order', () => {
    const base = 'The quick\r\nbrown fox';
    const opA = { op: insert(4, 'very '), baseVersion: 0, siteId: 1 };
    const opB = { op: del(10, 6), baseVersion: 0, siteId: 2 }; // "\nbrown"

    const s1 = new Session(base);
    s1.receive(opA);
    const fwdB = s1.receive(opB);

    const s2 = new Session(base);
    s2.receive(opB);
    const fwdA = s2.receive(opA);

    assert.equal(s1.doc, s2.doc);
    assert.equal(s1.doc, 'The very quick\r fox');
    // Each site's view of the other's op differs, but both apply legally.
    assert.notDeepEqual(fwdA, fwdB);
  });
});
