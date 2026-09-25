/**
 * Differential tests: the compiled, budgeted, resumable plan is cross-checked
 * against an independent slow interpreter over randomized structures —
 * including deep trees, array holes and malicious property names.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compile, evaluate, formatPath, type Match } from '../src/index.ts';
import { ParseError } from '../src/errors.ts';

// ---------- deterministic PRNG ----------
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- stable value serialization (holes, undefined, cycles) ----------
function stable(value: unknown, seen = new Set<unknown>()): string {
  if (value === undefined) return 'undefined';
  if (value === null || typeof value === 'number' || typeof value === 'string' || typeof value === 'boolean') {
    return JSON.stringify(value);
  }
  if (typeof value === 'function') return '<function>';
  if (typeof value === 'object') {
    if (seen.has(value)) return '<cycle>';
    seen.add(value);
    let out: string;
    if (Array.isArray(value)) {
      const parts: string[] = [];
      for (let i = 0; i < value.length; i++) {
        parts.push(Object.prototype.hasOwnProperty.call(value, i) ? stable(value[i], seen) : '<hole>');
      }
      out = `[${parts.join(',')}]`;
    } else {
      const keys = Object.keys(value).sort();
      out = `{${keys.map((k) => `${JSON.stringify(k)}:${stable((value as Record<string, unknown>)[k], seen)}`).join(',')}}`;
    }
    seen.delete(value);
    return out;
  }
  return String(value);
}

function normalize(matches: Match[]): string[] {
  return matches.map((m) => `${formatPath(m.path)}=${stable(m.value)}`).sort();
}

// ---------- random structure generation ----------
const KEY_POOL = ['a', 'b', 'c', 'id', 'name', 'x', '', 'a b', '构造', '__proto__', 'constructor', 'hasOwnProperty', '0', 'z'];

function genValue(rng: () => number, depth: number): unknown {
  const roll = rng();
  if (depth <= 0 || roll < 0.35) {
    const leaf = rng();
    if (leaf < 0.3) return Math.floor(rng() * 20);
    if (leaf < 0.5) return KEY_POOL[Math.floor(rng() * 6)];
    if (leaf < 0.6) return true;
    if (leaf < 0.7) return false;
    if (leaf < 0.8) return null;
    if (leaf < 0.9) return undefined; // explicit undefined
    return 's' + Math.floor(rng() * 100);
  }
  if (roll < 0.65) {
    // array, possibly sparse (holes)
    const len = Math.floor(rng() * 5);
    const arr: unknown[] = new Array(len);
    for (let i = 0; i < len; i++) {
      if (rng() < 0.2) continue; // leave a hole
      arr[i] = genValue(rng, depth - 1);
    }
    return arr;
  }
  const obj: Record<string, unknown> = {};
  const count = Math.floor(rng() * 5);
  for (let i = 0; i < count; i++) {
    const key = KEY_POOL[Math.floor(rng() * KEY_POOL.length)];
    const value = genValue(rng, depth - 1);
    // defineProperty keeps '__proto__'/'constructor' as own data properties
    Object.defineProperty(obj, key, { value, enumerable: true, writable: true, configurable: true });
  }
  return obj;
}

// ---------- random expression generation ----------
function genExpression(rng: () => number): string {
  let expr = '$';
  const segments = 1 + Math.floor(rng() * 4);
  for (let i = 0; i < segments; i++) {
    const kind = rng();
    if (kind < 0.4) {
      const key = KEY_POOL[Math.floor(rng() * KEY_POOL.length)];
      expr += /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(key) ? `.${key}` : `.'${key}'`;
    } else if (kind < 0.5) {
      expr += `[${Math.floor(rng() * 4)}]`;
    } else if (kind < 0.65) {
      expr += '[*]';
    } else if (kind < 0.85) {
      expr += `[?(${genFilter(rng)})]`;
    } else {
      const key = KEY_POOL[Math.floor(rng() * 8)]; // safe names only for descent
      expr += `..${key}`;
    }
  }
  return expr;
}

function genFilter(rng: () => number): string {
  const field = KEY_POOL[Math.floor(rng() * KEY_POOL.length)];
  const at = /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(field) ? `@.${field}` : `@.'${field}'`;
  const lit = (): string => {
    const r = rng();
    if (r < 0.4) return String(Math.floor(rng() * 20));
    if (r < 0.6) return `'k${Math.floor(rng() * 5)}'`;
    if (r < 0.7) return 'true';
    if (r < 0.8) return 'false';
    if (r < 0.9) return 'undefined';
    return 'null';
  };
  const cond = (): string => {
    const r = rng();
    if (r < 0.3) return `${at} == ${lit()}`;
    if (r < 0.45) return `${at} != ${lit()}`;
    if (r < 0.6) return `${at} < ${Math.floor(rng() * 20)}`;
    if (r < 0.7) return `${at} >= ${Math.floor(rng() * 20)}`;
    if (r < 0.8) return `${at} in [${lit()}, ${lit()}]`;
    if (r < 0.9) return `exists(${at})`;
    return at;
  };
  const r = rng();
  if (r < 0.6) return cond();
  if (r < 0.8) return `${cond()} && ${cond()}`;
  return `${cond()} || !(${cond()})`;
}

// ---------- differential run ----------
test('compiled plan and slow interpreter agree on 400 randomized cases', () => {
  let parseErrors = 0;
  for (let seed = 1; seed <= 400; seed++) {
    const rng = mulberry32(seed);
    const doc = genValue(rng, 5);
    const expr = genExpression(rng);
    let plan;
    try {
      plan = compile(expr);
    } catch (err) {
      assert.ok(err instanceof ParseError, `seed ${seed}: non-parse error for ${expr}: ${err}`);
      parseErrors++;
      continue;
    }
    const fast = plan.run(doc, { maxTotalSteps: 1_000_000 });
    const slow = evaluate(plan.ast, doc, expr);
    assert.deepEqual(
      normalize(fast),
      normalize(slow),
      `seed ${seed}: mismatch for expression ${expr}`,
    );
  }
  assert.ok(parseErrors > 0, 'random generator should exercise parse-time denials');
});

test('deep trees: both engines agree, and budget bounds the compiled plan', () => {
  const DEPTH = 3000;
  let node: Record<string, unknown> = { target: 'found' };
  for (let i = 0; i < DEPTH; i++) node = { next: node };
  const plan = compile('$..target');
  const fast = plan.run(node, { maxTotalSteps: 1_000_000 });
  const slow = evaluate(plan.ast, node, '$..target');
  assert.deepEqual(normalize(fast), normalize(slow));
  assert.equal(fast.length, 1);
  assert.equal(formatPath(fast[0].path).split('next').length - 1, DEPTH);
  assert.throws(() => plan.run(node, { maxTotalSteps: 100 }), /step budget exhausted/);
});

test('malicious property names never leak through either engine', () => {
  const doc = JSON.parse(`{
    "__proto__": {"polluted": true},
    "constructor": {"prototype": {"x": 1}},
    "safe": {"__proto__": {"y": 2}, "ok": 3}
  }`);
  for (const expr of ['$[*]', '$..polluted', '$..x', '$..y', '$.safe[*]', '$[*][*]']) {
    const plan = compile(expr);
    const fast = normalize(plan.run(doc));
    const slow = normalize(evaluate(plan.ast, doc, expr));
    assert.deepEqual(fast, slow, expr);
    // denied keys must never appear in any *path* (traversal is blocked);
    // a legitimately matched value may still contain such keys as inert data.
    const paths = plan.run(doc).map((m) => formatPath(m.path));
    for (const p of paths) {
      assert.ok(!p.includes('__proto__'), `${expr} leaked __proto__ into path: ${p}`);
      assert.ok(!p.includes('polluted'), `${expr} leaked polluted into path: ${p}`);
    }
  }
  assert.deepEqual(compile('$.safe[*]').run(doc).map((m) => m.path[1]), ['ok']);
});

test('sliced resumable execution matches the slow interpreter exactly', () => {
  const rng = mulberry32(987654);
  const doc = genValue(rng, 6);
  const expr = '$..id';
  const plan = compile(expr);
  const slow = normalize(evaluate(plan.ast, doc, expr));
  const execution = plan.start(doc, { maxTotalSteps: 1_000_000 });
  let done = false;
  for (let guard = 0; guard < 2_000_000 && !done; guard++) {
    done = execution.resume(1).done;
  }
  assert.ok(done);
  assert.deepEqual(normalize(execution.resume(0).matches), slow);
});
