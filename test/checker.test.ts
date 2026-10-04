import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkEquivalence, symbolicCheck } from '../src/checker.ts';
import { evaluate } from '../src/interp.ts';
import { parse } from '../src/parser.ts';
import type { CheckResult } from '../src/result.ts';

const p = parse;

function assertEqual(a: string, b: string, width: number): void {
  const r = symbolicCheck(p(a), p(b), width);
  assert.equal(r.status, 'equal', `${a} vs ${b} @${width}: ${JSON.stringify(r)}`);
}

function assertNotEqual(
  a: string,
  b: string,
  width: number,
  expected: Record<string, bigint>,
): void {
  const r: CheckResult = symbolicCheck(p(a), p(b), width);
  assert.equal(r.status, 'not-equal', `${a} vs ${b} @${width}`);
  if (r.status !== 'not-equal') return;
  assert.deepEqual(r.counterexample, expected);
  // The reported counterexample must really distinguish the expressions.
  assert.notEqual(
    evaluate(p(a), r.counterexample, width),
    evaluate(p(b), r.counterexample, width),
  );
}

const IDENTITIES: Array<[string, string]> = [
  ['x + y', 'y + x'],
  ['(x + y) - y', 'x'],
  ['(x ^ y) ^ y', 'x'],
  ['(x & y) | (x & ~y)', 'x'],
  ['x << 1', 'x + x'],
  ['-x', '(~x) + 1'],
  ['x - y', 'x + (~y) + 1'],
  ['~(x | y)', '(~x) & (~y)'],
  ['(x ? 1 : 0) + (x ? 0 : 1)', '1'],
  ['(x >> 1) << 1', 'x & (~1)'],
  ['(x + y) << 1', '(x << 1) + (y << 1)'],
  ['x & (x - 1)', '(x & (x - 1)) | 0'],
];

test('algebraic identities hold at widths 1..8 (symbolic proof)', () => {
  for (const [a, b] of IDENTITIES) {
    for (let width = 1; width <= 8; width++) {
      assertEqual(a, b, width);
    }
  }
});

test('identities also hold at larger widths via the symbolic engine', () => {
  for (const width of [16, 32, 64]) {
    assertEqual('(x + y) - y', 'x', width);
    assertEqual('x - y', 'x + (~y) + 1', width);
    assertEqual('(x & y) | (x & ~y)', 'x', width);
  }
});

test('shift rules: amount >= width is zero', () => {
  for (let width = 1; width <= 8; width++) {
    assertEqual(`x << ${width}`, '0', width); // amount == width
  }
  // The amount literal is itself truncated to the bit width, so the
  // "amount > width" cases need amounts representable in the width.
  for (let width = 2; width <= 8; width++) {
    assertEqual(`x << ${width + 1}`, '0', width); // amount > width
  }
  for (let width = 3; width <= 8; width++) {
    assertEqual(`x >> ${width + 3}`, '0', width); // amount > width
  }
});

test('lexicographically smallest counterexamples', () => {
  assertNotEqual('x + 1', 'x + 2', 8, { x: 0n });
  assertNotEqual('x & y', 'x | y', 4, { x: 0n, y: 1n });
  assertNotEqual('x - y', 'y - x', 4, { x: 0n, y: 1n });
  assertNotEqual('x << y', 'x << (y & 1)', 4, { x: 1n, y: 2n });
  assertNotEqual('x >> y', 'x >> (y & 3)', 4, { x: 1n, y: 4n });
  assertNotEqual('(x & 1) ? y : 0', 'y', 4, { x: 0n, y: 1n });
});

test('counterexample minimization respects variable order (sorted names)', () => {
  // Both (a=0,b=1) and (a=1,b=0) distinguish the expressions; the tuple
  // order is (a, b), so (0, 1) is lexicographically smaller.
  assertNotEqual('a & b', 'a | b', 4, { a: 0n, b: 1n });
  // Same pair with reversed roles: now (x=0, y=1) must win too.
  assertNotEqual('y & x', 'y | x', 4, { x: 0n, y: 1n });
});

test('budget exhaustion yields unknown, never a wrong answer', () => {
  const r = symbolicCheck(p('(x + y) - y'), p('x'), 8, { maxPropagations: 0 });
  assert.equal(r.status, 'unknown');
  const r2 = symbolicCheck(p('x + 1'), p('x + 2'), 8, { maxPropagations: 0 });
  assert.equal(r2.status, 'unknown');
});

test('auto engine: small spaces proven exhaustively, large ones symbolically', () => {
  const small = checkEquivalence(p('x + 1'), p('x + 2'), { width: 4 });
  assert.equal(small.status, 'not-equal');
  const large = checkEquivalence(p('(x + y) - y'), p('x'), { width: 32 });
  assert.equal(large.status, 'equal');
  const largeNe = checkEquivalence(p('x << y'), p('y << x'), { width: 32 });
  assert.deepEqual(largeNe, { status: 'not-equal', counterexample: { x: 0n, y: 1n } });
});
