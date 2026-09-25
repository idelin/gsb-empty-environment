import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parsePath } from '../src/parser.ts';
import { compileAst } from '../src/plan.ts';
import { executePlan, type Match } from '../src/vm.ts';
import { evaluateSlow } from '../src/interpreter.ts';

/** Deterministic PRNG (mulberry32) so failures are reproducible. */
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

// Malicious and awkward property names mixed into the DATA (never into
// generated path expressions, which would be rejected at parse time).
const KEY_POOL = [
  'a', 'b', 'c', 'name', 'n', 'tag', 'items', 'x',
  'hasOwnProperty', 'toString', 'valueOf', 'length', '__proto__',
  'constructor', 'prototype', '', '0', 'a b', '键', '$',
];
const SAFE_KEY_POOL = KEY_POOL.filter(
  (k) => !['__proto__', 'constructor', 'prototype', ''].includes(k) && !/^\d+$/.test(k),
);
// Keys that are also valid as dot-notation identifiers in path expressions.
const PATH_KEY_POOL = SAFE_KEY_POOL.filter((k) => /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(k));

function setAny(obj: Record<string, unknown>, key: string, value: unknown): void {
  // Object.defineProperty creates even '__proto__' as a real own property.
  Object.defineProperty(obj, key, {
    value,
    enumerable: true,
    configurable: true,
    writable: true,
  });
}

function randomLeaf(r: () => number): unknown {
  const pick = r();
  if (pick < 0.3) return Math.floor(r() * 20) - 10;
  if (pick < 0.55) return ['a', 'b', 'tag', 'x'][Math.floor(r() * 4)];
  if (pick < 0.7) return r() < 0.5;
  if (pick < 0.8) return null;
  if (pick < 0.9) return undefined; // explicit undefined, distinct from holes
  return Math.floor(r() * 100);
}

function randomTree(r: () => number, depth: number): unknown {
  if (depth <= 0 || r() < 0.35) return randomLeaf(r);
  if (r() < 0.4) {
    // Array, sometimes with holes.
    const len = Math.floor(r() * 5);
    const arr: unknown[] = new Array(len);
    for (let i = 0; i < len; i++) {
      if (r() < 0.25) continue; // leave a hole
      arr[i] = randomTree(r, depth - 1);
    }
    return arr;
  }
  const obj: Record<string, unknown> = {};
  const count = Math.floor(r() * 5);
  for (let i = 0; i < count; i++) {
    const key = KEY_POOL[Math.floor(r() * KEY_POOL.length)]!;
    setAny(obj, key, randomTree(r, depth - 1));
  }
  return obj;
}

function randomPath(r: () => number): string {
  const segments = 1 + Math.floor(r() * 4);
  const parts: string[] = [];
  for (let i = 0; i < segments; i++) {
    const kind = r();
    if (kind < 0.4) {
      parts.push(`.${PATH_KEY_POOL[Math.floor(r() * PATH_KEY_POOL.length)]}`);
    } else if (kind < 0.55) {
      parts.push(`[${Math.floor(r() * 4)}]`);
    } else if (kind < 0.7) {
      parts.push('[*]');
    } else if (kind < 0.85) {
      parts.push(`..${PATH_KEY_POOL[Math.floor(r() * PATH_KEY_POOL.length)]}`);
    } else {
      const key = PATH_KEY_POOL[Math.floor(r() * PATH_KEY_POOL.length)];
      const lit = Math.floor(r() * 10) - 5;
      const op = ['==', '!=', '<', '<=', '>', '>='][Math.floor(r() * 6)];
      parts.push(`[?(@.${key} ${op} ${lit})]`);
    }
  }
  return parts.join('');
}

function normalize(matches: Match[]): string[] {
  return matches
    .map((m) => {
      const tag =
        m.value === undefined
          ? '<undefined>'
          : JSON.stringify(m.value) ?? '<unserializable>';
      return `${JSON.stringify(m.path)}=${tag}`;
    })
    .sort();
}

test('compiled plan matches slow interpreter on random hostile trees', () => {
  const BUDGET = 1_000_000;
  for (let seed = 1; seed <= 60; seed++) {
    const r = rng(seed);
    const doc = randomTree(r, 10); // deep trees
    for (let q = 0; q < 8; q++) {
      const source = randomPath(r);
      const ast = parsePath(source);
      const fast = executePlan(compileAst(ast), doc, BUDGET);
      const slow = evaluateSlow(ast, doc, BUDGET);
      assert.deepEqual(
        normalize(fast),
        normalize(slow),
        `seed=${seed} path=${source}`,
      );
    }
  }
});

test('cross-check on deep chains, holes and malicious names (targeted)', () => {
  const BUDGET = 1_000_000;
  let deep: Record<string, unknown> = {};
  const root = deep;
  for (let i = 0; i < 28; i++) {
    const next: Record<string, unknown> = {};
    setAny(deep, 'k', next);
    deep = next;
  }
  setAny(deep, 'n', 42);

  const hostile: Record<string, unknown> = {};
  setAny(hostile, '__proto__', { n: 1 });
  setAny(hostile, 'constructor', { n: 2 });
  setAny(hostile, 'hasOwnProperty', { n: 3 });
  const sparse: unknown[] = new Array(4);
  sparse[1] = { n: 7 };
  sparse[3] = undefined;
  setAny(hostile, 'arr', sparse);

  const doc = { root, hostile };
  const paths = [
    'root' + '.k'.repeat(28) + '.n',
    '..n',
    'hostile.hasOwnProperty.n',
    'hostile.arr[*].n',
    'hostile.arr[3]',
    'hostile.arr[1]',
  ];
  for (const source of paths) {
    const ast = parsePath(source);
    const fast = executePlan(compileAst(ast), doc, BUDGET);
    const slow = evaluateSlow(ast, doc, BUDGET);
    assert.deepEqual(normalize(fast), normalize(slow), source);
  }
  // Spot-check expectations.
  assert.deepEqual(
    executePlan(compileAst(parsePath('hostile.arr[*].n')), doc, BUDGET).map((m) => m.path),
    [['hostile', 'arr', 1, 'n']],
  );
  assert.deepEqual(
    executePlan(compileAst(parsePath('hostile.arr[3]')), doc, BUDGET).map((m) => m.value),
    [undefined],
  );
  assert.deepEqual(
    executePlan(compileAst(parsePath('hostile.arr[2]')), doc, BUDGET),
    [],
    'hole is absent',
  );
});
