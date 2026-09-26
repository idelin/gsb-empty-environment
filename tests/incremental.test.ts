import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalJsonParser,
  parseJson,
  parseJsonEvents,
  buildJsonValue,
  isJsonNumber,
  JsonSyntaxError,
} from '../src/incremental.ts';
import { DecimalError } from '../src/decimal.ts';
import type { JsonEvent, JsonValue } from '../src/incremental.ts';

function snapValue(v: JsonValue): unknown {
  if (v === null || typeof v === 'boolean' || typeof v === 'string') return v;
  if (Array.isArray(v)) return v.map(snapValue);
  if (isJsonNumber(v)) {
    return ['num', v.decimal.raw, v.decimal.digits, v.decimal.exp, v.decimal.neg];
  }
  return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, snapValue(x)]));
}

function snapshot(events: JsonEvent[]): unknown[] {
  return events.map((e) => {
    if (e.type === 'value') return ['value', snapValue(e.value)];
    if (e.type === 'key') return ['key', e.value];
    return [e.type];
  });
}

function parseChunked(doc: string, splits: number[]): JsonEvent[] {
  const p = new IncrementalJsonParser();
  const out: JsonEvent[] = [];
  let prev = 0;
  for (const i of splits) {
    out.push(...p.write(doc.slice(prev, i)));
    prev = i;
  }
  out.push(...p.write(doc.slice(prev)));
  out.push(...p.end());
  return out;
}

const DOCS = [
  '[1, -0, 1e+02, 9007199254740993, "x", {"a": [true, null, 0.5]}]',
  '{"n": 1.5e-7, "m": -0.0, "s": "a\\u0041\\n\\t\\\\"}',
  '123456789012345678901234567890',
  '-3.14000e+10',
  '{"empty": {}, "arr": [], "esc": "\\"\\\\/\\b\\f\\r"}',
  'true',
  '"plain string"',
  'null',
];

test('chunked parsing is consistent with one-shot at every split point', () => {
  for (const doc of DOCS) {
    const base = snapshot(parseJsonEvents(doc));
    for (let i = 0; i <= doc.length; i++) {
      assert.deepEqual(snapshot(parseChunked(doc, [i])), base, `split at ${i} of ${doc}`);
    }
  }
});

test('random multi-way splits are consistent with one-shot', () => {
  let seed = 0x2f6e2b1;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed;
  };
  for (const doc of DOCS) {
    const base = snapshot(parseJsonEvents(doc));
    for (let trial = 0; trial < 200; trial++) {
      const cuts = new Set<number>();
      const nCuts = rand() % 8;
      for (let k = 0; k < nCuts; k++) cuts.add(rand() % (doc.length + 1));
      const splits = [...cuts].sort((a, b) => a - b);
      assert.deepEqual(snapshot(parseChunked(doc, splits)), base, `splits ${splits} of ${doc}`);
    }
  }
});

test('tree built from chunked events equals one-shot parse', () => {
  for (const doc of DOCS) {
    const whole = parseJson(doc);
    for (let i = 0; i <= doc.length; i += 3) {
      assert.deepEqual(snapValue(buildJsonValue(parseChunked(doc, [i]))), snapValue(whole));
    }
  }
});

test('never publishes half a number; raw fragment accumulates across chunks', () => {
  const p = new IncrementalJsonParser();
  assert.deepEqual(snapshot(p.write('[12')), [['startArray']]);
  assert.deepEqual(snapshot(p.write('3')), [], 'number still incomplete: no event');
  const ev = p.write('4]');
  assert.deepEqual(snapshot(ev), [
    ['value', ['num', '1234', '1234', 4, false]],
    ['endArray'],
  ]);
  p.end();

  const q = new IncrementalJsonParser();
  const all: JsonEvent[] = [];
  for (const ch of '[1e+02]') all.push(...q.write(ch)); // one byte at a time
  q.end();
  const nums = all.filter((e) => e.type === 'value');
  assert.equal(nums.length, 1, 'exactly one number event, only after the delimiter');
  assert.deepEqual(snapshot(nums), [['value', ['num', '1e+02', '1', 3, false]]]);
});

test('tokens split across chunk boundaries: strings, unicode escapes, literals', () => {
  const doc = '["a\\u0041b", true, null, false]';
  const base = snapshot(parseJsonEvents(doc));
  for (let i = 0; i <= doc.length; i++) {
    assert.deepEqual(snapshot(parseChunked(doc, [i])), base, `split at ${i}`);
  }
  const tree = parseJson(doc) as JsonValue[];
  assert.equal(tree[0], 'aAb');
});

test('syntax errors carry offsets; leading zeros and non-finite literals rejected', () => {
  let err: JsonSyntaxError | null = null;
  try { parseJson('[1,]'); } catch (e) { err = e as JsonSyntaxError; }
  assert.ok(err instanceof JsonSyntaxError);
  assert.equal(err.offset, 3);
  assert.throws(() => parseJson('{"a":01}'), JsonSyntaxError);
  assert.throws(() => parseJson('01'), JsonSyntaxError);
  assert.throws(() => parseJson('[NaN]'), JsonSyntaxError);
  assert.throws(() => parseJson('[-Infinity]'), JsonSyntaxError);
  assert.throws(() => parseJson('[1e]'), JsonSyntaxError);
  assert.throws(() => parseJson('[1.'), JsonSyntaxError);
  assert.throws(() => parseJson('{"a":"x'), JsonSyntaxError);
  assert.throws(() => parseJson('[1,2'), JsonSyntaxError);
  assert.throws(() => parseJson(''), JsonSyntaxError);
  assert.throws(() => parseJson('1 2'), JsonSyntaxError);
  assert.throws(() => parseJson('{"a":1,}'), JsonSyntaxError);
  assert.throws(() => parseJson('["\\q"]'), JsonSyntaxError);
});

test('precision budget applies to chunked numbers too', () => {
  const p = new IncrementalJsonParser({ maxSignificantDigits: 4 });
  p.write('[123');
  assert.throws(() => p.write('45]'), DecimalError);
});
