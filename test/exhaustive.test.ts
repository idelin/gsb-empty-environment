import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verifyExhaustive } from '../src/enumerate.ts';
import type { Caps } from '../src/enumerate.ts';

const CAPS: Caps = { maxLen: 2, maxValues: 500, maxMessages: 100000 };

const rec = (fields: unknown[]) => ({ kind: 'record', fields });
const fld = (id: number, name: string, required: boolean, type: unknown, extra: object = {}) => ({
  id, name, required, type, ...extra,
});
const int = (min: number, max: number) => ({ kind: 'int64', min, max });
const uint = (min: number, max: number) => ({ kind: 'uint64', min, max });
const str = (maxLen: number) => ({ kind: 'string', maxLen });
const byt = (maxLen: number) => ({ kind: 'bytes', maxLen });
const arr = (element: unknown, maxLen: number) => ({ kind: 'array', element, maxLen });

const match = (oldS: unknown, newS: unknown) => {
  const r = verifyExhaustive(oldS, newS, CAPS);
  assert.equal(r.result, 'match', JSON.stringify(r));
  return r;
};

test('exhaustive: directed pairs agree with the checker', () => {
  // rename
  match(rec([fld(1, 'a', true, int(0, 3))]), rec([fld(1, 'b', true, int(0, 3))]));
  // narrowing in both directions
  match(rec([fld(1, 'n', true, int(0, 4))]), rec([fld(1, 'n', true, int(1, 3))]));
  match(rec([fld(1, 'n', true, int(-2, 2))]), rec([fld(1, 'n', true, int(-1, 1))]));
  match(rec([fld(1, 's', true, str(2))]), rec([fld(1, 's', true, str(1))]));
  match(rec([fld(1, 'b', true, byt(2))]), rec([fld(1, 'b', true, byt(1))]));
  match(rec([fld(1, 'a', true, arr(int(0, 2), 2))]), rec([fld(1, 'a', true, arr(int(0, 2), 1))]));
  match(rec([fld(1, 'a', true, arr(int(0, 2), 2))]), rec([fld(1, 'a', true, arr(int(0, 1), 2))]));
  // optional -> required, required -> optional
  match(rec([fld(1, 'n', false, int(0, 2))]), rec([fld(1, 'n', true, int(0, 2))]));
  match(rec([fld(1, 'n', true, int(0, 2))]), rec([fld(1, 'n', false, int(0, 2))]));
  // default invalidation: changed / removed / preserved
  match(rec([fld(1, 'n', false, int(0, 3), { default: 1 })]), rec([fld(1, 'n', false, int(0, 3), { default: 2 })]));
  match(rec([fld(1, 'n', false, int(0, 3), { default: 1 })]), rec([fld(1, 'n', false, int(0, 3))]));
  match(rec([fld(1, 'n', false, int(0, 3), { default: 1 })]), rec([fld(1, 'n', false, int(0, 3), { default: 1 })]));
  // deep nesting
  const deep = (max: number) =>
    rec([fld(1, 'one', true, rec([fld(2, 'two', true, arr(rec([fld(3, 'three', true, int(0, max))]), 1))]))]);
  match(deep(2), deep(1));
  match(deep(1), deep(2));
  // cross-kind numerics
  match(rec([fld(1, 'n', true, int(-2, 2))]), rec([fld(1, 'n', true, uint(0, 4))]));
  match(rec([fld(1, 'n', true, uint(0, 2))]), rec([fld(1, 'n', true, int(-2, 4))]));
  // kind change
  match(rec([fld(1, 'x', true, str(1))]), rec([fld(1, 'x', true, int(0, 2))]));
  // dropped / added fields
  match(rec([fld(1, 'a', true, int(0, 2)), fld(2, 'b', true, str(1))]), rec([fld(1, 'a', true, int(0, 2))]));
  match(rec([fld(1, 'a', true, int(0, 2))]), rec([fld(1, 'a', true, int(0, 2)), fld(2, 'b', true, str(1))]));
});

// ---- randomized cross-check on finite small domains ----

function mulberry32(seed: number) {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const pick = <T>(rnd: () => number, xs: T[]): T => xs[Math.floor(rnd() * xs.length)];

function randType(rnd: () => number, depth: number): Record<string, unknown> {
  const kinds = depth >= 2
    ? ['bool', 'int64', 'uint64', 'string', 'bytes']
    : ['bool', 'int64', 'uint64', 'string', 'bytes', 'array', 'record'];
  switch (pick(rnd, kinds)) {
    case 'bool':
      return { kind: 'bool' };
    case 'int64': {
      const min = -3 + Math.floor(rnd() * 6);
      return { kind: 'int64', min, max: min + Math.floor(rnd() * 5) };
    }
    case 'uint64': {
      const min = Math.floor(rnd() * 3);
      return { kind: 'uint64', min, max: min + Math.floor(rnd() * 4) };
    }
    case 'string':
      return { kind: 'string', maxLen: Math.floor(rnd() * 3) };
    case 'bytes':
      return { kind: 'bytes', maxLen: Math.floor(rnd() * 3) };
    case 'array':
      return { kind: 'array', element: randType(rnd, depth + 1), maxLen: Math.floor(rnd() * 3) };
    default:
      return randRecord(rnd, depth + 1);
  }
}

function randValue(rnd: () => number, t: Record<string, unknown>, depth: number): unknown {
  switch (t.kind) {
    case 'bool':
      return rnd() < 0.5;
    case 'int64':
    case 'uint64': {
      const min = t.min as number;
      const max = t.max as number;
      return min + Math.floor(rnd() * (max - min + 1));
    }
    case 'string': {
      const n = Math.floor(rnd() * ((t.maxLen as number) + 1));
      return Array.from({ length: n }, () => pick(rnd, [' ', 'a', 'b'])).join('');
    }
    case 'bytes': {
      const n = Math.floor(rnd() * ((t.maxLen as number) + 1));
      return Buffer.from(Array.from({ length: n }, () => pick(rnd, [0, 0xf8, 0xfb, 0xed, 0xff]))).toString('base64');
    }
    case 'array': {
      const n = Math.floor(rnd() * ((t.maxLen as number) + 1));
      return Array.from({ length: n }, () => randValue(rnd, t.element as Record<string, unknown>, depth + 1));
    }
    case 'record': {
      const out: Record<string, unknown> = {};
      for (const f of t.fields as { id: number; required: boolean; type: Record<string, unknown> }[]) {
        if (f.required || rnd() < 0.5) out[String(f.id)] = randValue(rnd, f.type, depth + 1);
      }
      return out;
    }
    default:
      throw new Error('bad kind');
  }
}

function randRecord(rnd: () => number, depth: number): Record<string, unknown> {
  const ids = [1, 2, 3, 4, 5].sort(() => rnd() - 0.5);
  const n = 1 + Math.floor(rnd() * 3);
  const fields = [];
  for (let i = 0; i < n; i++) {
    const type = randType(rnd, depth);
    const required = rnd() < 0.6;
    const f: Record<string, unknown> = { id: ids[i], name: `f${ids[i]}_${Math.floor(rnd() * 100)}`, required, type };
    if (!required && rnd() < 0.4) f.default = randValue(rnd, type, depth);
    fields.push(f);
  }
  return { kind: 'record', fields };
}

test('exhaustive: randomized schema pairs agree with the checker', () => {
  let matched = 0;
  let tooBig = 0;
  for (let seed = 1; seed <= 400; seed++) {
    const rnd = mulberry32(seed);
    const oldS = randRecord(rnd, 0);
    const newS = randRecord(rnd, 0);
    const r = verifyExhaustive(oldS, newS, CAPS);
    if (r.result === 'too-big') {
      tooBig++;
      continue;
    }
    assert.equal(r.result, 'match', `seed=${seed}\n${JSON.stringify(r, null, 2)}\nold=${JSON.stringify(oldS)}\nnew=${JSON.stringify(newS)}`);
    matched++;
  }
  assert.ok(matched >= 200, `only ${matched} matched (${tooBig} too big)`);
});

test('exhaustive: string/bytes domain overlap pairs', () => {
  match(rec([fld(1, 's', true, str(0))]), rec([fld(1, 's', true, byt(1))]));
  match(rec([fld(1, 's', true, str(1))]), rec([fld(1, 's', true, byt(2))]));
  match(rec([fld(1, 'b', true, byt(1))]), rec([fld(1, 'b', true, str(2))]));
  match(rec([fld(1, 'b', true, byt(1))]), rec([fld(1, 'b', true, str(1))]));
  match(rec([fld(1, 'b', true, byt(0))]), rec([fld(1, 'b', true, str(0))]));
});
