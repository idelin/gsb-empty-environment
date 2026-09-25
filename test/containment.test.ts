import test from 'node:test';
import assert from 'node:assert/strict';
import {
  and, between, contains, diff, FALSE, ge, gt, inSet, isEmpty, le, not, or, TRUE,
  type Domain, type Point,
} from '../src/index.ts';

const intDomain = (lo: number, hi: number, optional = false): Domain => ({
  x: { kind: 'int', lo, hi, optional },
});

test('x>=3 does not contain x>=2; minimal counterexample is x=2', () => {
  const result = contains(ge('x', 3), ge('x', 2), intDomain(0, 10));
  assert.equal(result.status, 'not-contained');
  assert.deepEqual((result as { counterexample: Point }).counterexample, { x: 2 });
});

test('x>=2 contains x>=3', () => {
  assert.equal(contains(ge('x', 2), ge('x', 3), intDomain(0, 10)).status, 'contained');
});

test('empty range is contained in any range', () => {
  const empty = and(ge('x', 5), le('x', 3));
  assert.equal(contains(ge('x', 0), empty, intDomain(0, 10)).status, 'contained');
  assert.equal(contains(FALSE, empty, intDomain(0, 10)).status, 'contained');
  assert.equal(isEmpty(empty, intDomain(0, 10)).status, 'contained');
});

test('empty domain is contained in anything', () => {
  const domain: Domain = { x: { kind: 'int', lo: 5, hi: 2 } };
  assert.equal(contains(ge('x', 100), ge('x', 3), domain).status, 'contained');
});

test('attribute without a declared domain cannot be proven', () => {
  const result = contains(ge('y', 3), ge('y', 2), intDomain(0, 10));
  assert.equal(result.status, 'undecided');
  assert.equal((result as { reason: string }).reason, 'unknown-domain');
});

test('difference constraints over two integer variables', () => {
  const domain: Domain = { x: { kind: 'int', lo: 0, hi: 10 }, y: { kind: 'int', lo: 0, hi: 10 } };
  // x-y<=3 does not contain x-y<=5; minimal violation is x-y=4 at x=4,y=0.
  const wide = contains(diff('x', '<=', 'y', 3), diff('x', '<=', 'y', 5), domain);
  assert.equal(wide.status, 'not-contained');
  assert.deepEqual((wide as { counterexample: Point }).counterexample, { x: 4, y: 0 });
  // x-y<=5 contains x-y<=3.
  assert.equal(contains(diff('x', '<=', 'y', 5), diff('x', '<=', 'y', 3), domain).status, 'contained');
  // Combined with bounds: (x-y<=4 and y>=1) is contained in x-y<=5.
  assert.equal(
    contains(diff('x', '<=', 'y', 5), and(diff('x', '<=', 'y', 4), ge('y', 1)), domain).status,
    'contained',
  );
});

test('finite set predicates', () => {
  const domain: Domain = { role: { kind: 'enum', values: ['admin', 'editor', 'viewer'] } };
  assert.equal(
    contains(inSet('role', ['admin', 'editor']), inSet('role', ['admin']), domain).status,
    'contained',
  );
  const reverse = contains(inSet('role', ['admin']), inSet('role', ['admin', 'editor']), domain);
  assert.equal(reverse.status, 'not-contained');
  // 'editor' precedes 'viewer' in declaration order, but only 'editor' violates.
  assert.deepEqual((reverse as { counterexample: Point }).counterexample, { role: 'editor' });
});

test('boolean combinations (and / or / not)', () => {
  const domain = intDomain(0, 10);
  // (x>=2 and x<=8) contains (x>=4 and x<=6)
  assert.equal(contains(between('x', 2, 8), between('x', 4, 6), domain).status, 'contained');
  // not(x>=8) contains x<=5
  assert.equal(contains(not(ge('x', 8)), le('x', 5), domain).status, 'contained');
  // (x>=7 or x<=1) does not contain (x<=2 or x>=6): minimal violation is x=6? no...
  const r = contains(or(ge('x', 7), le('x', 1)), or(le('x', 2), ge('x', 6)), domain);
  assert.equal(r.status, 'not-contained');
  // violations: containee true & container not true -> x in {2, 6}; minimal is 2.
  assert.deepEqual((r as { counterexample: Point }).counterexample, { x: 2 });
});

test('three-valued semantics: unknown attribute makes container not definitely true', () => {
  const domain = intDomain(0, 5, true); // optional: x may be unknown (⊥)
  const result = contains(ge('x', 1), TRUE, domain);
  assert.equal(result.status, 'not-contained');
  assert.deepEqual((result as { counterexample: Point }).counterexample, { x: null });
});

test('identical scopes contain each other even with unknown attributes', () => {
  const domain = intDomain(0, 5, true);
  assert.equal(contains(ge('x', 1), and(ge('x', 1), le('x', 4)), domain).status, 'contained');
  assert.equal(contains(ge('x', 2), ge('x', 2), domain).status, 'contained');
});

test('budget exhaustion returns undecided', () => {
  const domain = intDomain(0, 10_000);
  const tight = contains(ge('x', 5000), ge('x', 4000), domain, { budget: 3 });
  assert.equal(tight.status, 'undecided');
  assert.equal((tight as { reason: string }).reason, 'budget');
  // A generous budget decides the same query with counterexample x=4000.
  const decided = contains(ge('x', 5000), ge('x', 4000), domain);
  assert.equal(decided.status, 'not-contained');
  assert.deepEqual((decided as { counterexample: Point }).counterexample, { x: 4000 });
});

test('set algebra: difference emptiness decides containment', () => {
  const domain = intDomain(0, 10);
  // x>=2 \ x>=3 is non-empty (x=2), so x>=3 does not contain x>=2.
  const d = contains(FALSE, and(ge('x', 2), not(ge('x', 3))), domain);
  assert.equal(d.status, 'not-contained');
  assert.deepEqual((d as { counterexample: Point }).counterexample, { x: 2 });
});

test('mixed integer, set and difference conditions', () => {
  const domain: Domain = {
    level: { kind: 'int', lo: 0, hi: 10 },
    clearance: { kind: 'int', lo: 0, hi: 10 },
    dept: { kind: 'enum', values: ['eng', 'ops', 'fin'] },
  };
  const senior = and(ge('level', 5), inSet('dept', ['eng', 'ops']));
  const allowed = and(diff('clearance', '>=', 'level', 0), inSet('dept', ['eng', 'ops', 'fin']));
  // senior staff whose clearance lags their level are not covered.
  const r = contains(allowed, and(senior, diff('clearance', '>=', 'level', 0)), domain);
  assert.equal(r.status, 'contained');
  const r2 = contains(allowed, senior, domain);
  assert.equal(r2.status, 'not-contained');
  // minimal: level=5 first (declaration order level, clearance, dept), clearance=0, dept=eng
  assert.deepEqual((r2 as { counterexample: Point }).counterexample, { level: 5, clearance: 0, dept: 'eng' });
});

test('type errors are rejected for malformed policies', () => {
  const domain: Domain = { role: { kind: 'enum', values: ['a'] } };
  assert.throws(() => contains(ge('role', 1), TRUE, domain), TypeError);
});
