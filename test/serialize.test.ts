import test from 'node:test';
import assert from 'node:assert/strict';
import { ErrorDigest } from '../src/digest.ts';
import { encode, decode } from '../src/serialize.ts';
import type { ValidationError } from '../src/types.ts';

const err = (over: Partial<ValidationError>): ValidationError => ({
  schemaVersion: 'v1',
  keyword: 'minimum',
  schemaPath: '#/items/minimum',
  message: 'must be >= 1',
  instancePath: [0],
  ...over,
});

function sampleDigest(): ErrorDigest {
  const d = new ErrorDigest();
  for (let i = 1; i <= 10; i++) d.add(err({ instancePath: [i] })); // one compact range
  d.add(err({ instancePath: [3] })); // duplicate value -> explicit ids form
  d.add(err({ instancePath: ['meta'], keyword: 'required', message: 'missing' })); // literal
  return d;
}

test('round trip: decode(encode(digest)) equals the original error set', () => {
  const d = sampleDigest();
  const doc = encode(d);
  const restored = decode(JSON.parse(JSON.stringify(doc)));
  assert.deepEqual(restored, d.expandAll('insertion'));
  assert.equal(doc.count, d.count);
});

test('round trip preserves duplicates and literal paths', () => {
  const d = sampleDigest();
  const restored = decode(encode(d));
  assert.equal(restored.filter((e) => e.instancePath.join('/') === '3').length, 2);
  assert.ok(restored.some((e) => e.keyword === 'required' && e.instancePath.join('/') === 'meta'));
});

const mutations: Array<[string, (doc: any) => void]> = [
  ['wrong format tag', (d) => { d.format = 'error-digest/2'; }],
  ['inflated total count', (d) => { d.count += 1; }],
  ['deflated total count', (d) => { d.count -= 1; }],
  ['inflated group count', (d) => { d.groups[0].count += 1; }],
  ['range start > end', (d) => { d.groups[0].templates[0].runs[0] = { start: 5, end: 2, idStart: 0 }; }],
  ['negative range start', (d) => { d.groups[0].templates[0].runs[0] = { start: -1, end: 5, idStart: 0 }; }],
  ['non-integer range start', (d) => { d.groups[0].templates[0].runs[0] = { start: 1.5, end: 5, idStart: 0 }; }],
  ['overlapping runs', (d) => { d.groups[0].templates[0].runs.push({ start: 2, end: 3, idStart: 500 }); }],
  ['unsorted runs', (d) => { d.groups[0].templates[0].runs.unshift({ start: 8, end: 9, idStart: 600 }); }],
  ['ids length mismatch', (d) => { d.groups[0].templates[0].runs[0] = { start: 1, end: 5, ids: [[0]] }; }],
  ['empty ids sub-array', (d) => { d.groups[0].templates[0].runs[0] = { start: 1, end: 2, ids: [[0], []] }; }],
  ['duplicate ids across runs', (d) => {
    d.groups[0].templates[0].runs = [
      { start: 1, end: 2, idStart: 0 },
      { start: 3, end: 4, idStart: 1 },
    ];
    d.groups[0].count = 4;
    d.count = 5;
  }],
  ['negative idStart', (d) => { d.groups[0].templates[0].runs[0] = { start: 1, end: 3, idStart: -1 }; }],
  ['run without ids', (d) => { d.groups[0].templates[0].runs[0] = { start: 1, end: 3 }; }],
  ['empty literal ids', (d) => { d.groups[1].templates.find((t: any) => t.kind === 'literal').ids = []; }],
  ['invalid path segment', (d) => { d.groups[0].templates[0].prefix = [null]; }],
  ['unknown template kind', (d) => { d.groups[0].templates[0].kind = 'blob'; }],
  ['count as float', (d) => { d.count = 12.5; }],
];

for (const [name, mutate] of mutations) {
  test(`mutation rejected: ${name}`, () => {
    const doc = JSON.parse(JSON.stringify(encode(sampleDigest())));
    mutate(doc);
    assert.throws(() => decode(doc), /invalid digest document/);
  });
}

test('valid document with explicit ids form decodes', () => {
  const d = new ErrorDigest();
  const id1 = d.add(err({ instancePath: [1] }));
  const id2 = d.add(err({ instancePath: [2] }));
  const id3 = d.add(err({ instancePath: [2] })); // duplicate -> ids form
  const doc = encode(d);
  const t = doc.groups[0].templates[0];
  assert.equal(t.kind, 'range');
  if (t.kind === 'range') {
    assert.deepEqual(t.runs, [{ start: 1, end: 2, ids: [[id1], [id2, id3]] }]);
  }
  assert.equal(decode(doc).length, 3);
});
