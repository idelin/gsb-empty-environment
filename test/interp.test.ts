import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate } from '../src/interp.ts';
import { parse } from '../src/parser.ts';

const ev = (src: string, env: Record<string, bigint>, width: number): bigint =>
  evaluate(parse(src), env, width);

test('truncates every result to the bit width', () => {
  assert.equal(ev('15 + 1', {}, 4), 0n);
  assert.equal(ev('0 - 1', {}, 4), 15n);
  assert.equal(ev('255', {}, 4), 15n);
  assert.equal(ev('0xFF & 0x0F', {}, 8), 15n);
  assert.equal(ev('~0', {}, 4), 15n);
  assert.equal(ev('-1', {}, 4), 15n);
  assert.equal(ev('-(5)', {}, 4), 11n);
});

test('shift amount greater than width is fixed to zero', () => {
  assert.equal(ev('x << 5', { x: 1n }, 4), 0n);
  assert.equal(ev('x >> 5', { x: 15n }, 4), 0n);
  assert.equal(ev('x << 100', { x: 3n }, 4), 0n);
  // amount == width is naturally zero for logical shifts
  assert.equal(ev('x << 4', { x: 1n }, 4), 0n);
  assert.equal(ev('x >> 4', { x: 15n }, 4), 0n);
  // amounts below the width behave normally
  assert.equal(ev('x << 3', { x: 1n }, 4), 8n);
  assert.equal(ev('x >> 1', { x: 9n }, 4), 4n);
});

test('shift amount may itself be an expression', () => {
  assert.equal(ev('x << (y + 1)', { x: 1n, y: 2n }, 4), 8n);
  assert.equal(ev('x << (y + 1)', { x: 1n, y: 14n }, 4), 0n); // amount 15 >= width
  // The amount expression is itself truncated to the bit width: 15+1 wraps
  // to 0 at width 4, so this shifts by 0.
  assert.equal(ev('x << (y + 1)', { x: 1n, y: 15n }, 4), 1n);
});

test('conditional select tests for nonzero', () => {
  assert.equal(ev('x ? 10 : 20', { x: 0n }, 8), 20n);
  assert.equal(ev('x ? 10 : 20', { x: 3n }, 8), 10n);
  assert.equal(ev('(x & 1) ? y : z', { x: 5n, y: 1n, z: 2n }, 8), 1n);
  assert.equal(ev('(x & 1) ? y : z', { x: 4n, y: 1n, z: 2n }, 8), 2n);
});

test('operator precedence and associativity', () => {
  assert.equal(ev('1 + 2 << 3', {}, 8), 24n); // (1+2)<<3
  assert.equal(ev('1 | 2 & 3', {}, 8), 3n); // 1 | (2&3)
  assert.equal(ev('7 ^ 3 & 1', {}, 8), 6n); // 7 ^ (3&1)
  assert.equal(ev('10 - 3 - 2', {}, 8), 5n); // left assoc
  assert.equal(ev('x ? 1 : y ? 2 : 3', { x: 0n, y: 5n }, 8), 2n); // right assoc
  assert.equal(ev('~-1', {}, 4), 0n);
});
