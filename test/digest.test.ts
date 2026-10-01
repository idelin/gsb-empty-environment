import test from 'node:test';
import assert from 'node:assert/strict';
import { ErrorDigest } from '../src/digest.ts';
import { encode } from '../src/serialize.ts';
import type { ValidationError } from '../src/types.ts';

const err = (over: Partial<ValidationError>): ValidationError => ({
  schemaVersion: 'v1',
  keyword: 'minimum',
  schemaPath: '#/items/minimum',
  message: 'must be >= 1',
  instancePath: [0],
  ...over,
});

const rangeRuns = (digest: ErrorDigest, keyword = 'minimum') => {
  const doc = encode(digest);
  const group = doc.groups.find((g) => g.keyword === keyword);
  assert.ok(group);
  const t = group.templates[0];
  assert.equal(t.kind, 'range');
  return t.kind === 'range' ? t.runs : [];
};

test('items 1..100 failing the same rule compress into a single range', () => {
  const d = new ErrorDigest();
  for (let i = 1; i <= 100; i++) d.add(err({ instancePath: [i] }));
  assert.equal(d.count, 100);
  assert.deepEqual(rangeRuns(d), [{ start: 1, end: 100, idStart: 0 }]);
  assert.equal(d.expandAll().length, 100);
});

test('a missing middle item must not be written as continuous', () => {
  const d = new ErrorDigest();
  for (let i = 1; i <= 100; i++) {
    if (i === 50) continue;
    d.add(err({ instancePath: [i] }));
  }
  assert.deepEqual(rangeRuns(d), [
    { start: 1, end: 49, idStart: 0 },
    { start: 51, end: 100, idStart: 49 },
  ]);
  assert.equal(d.expandAll().length, 99);
});

test('different rule reasons are never merged', () => {
  const d = new ErrorDigest();
  d.add(err({ instancePath: [0], keyword: 'minimum', message: 'must be >= 1' }));
  d.add(err({ instancePath: [0], keyword: 'type', message: 'expected type integer' }));
  d.add(err({ instancePath: [0], keyword: 'minimum', message: 'must be >= 5' }));
  const summaries = d.summaries();
  assert.equal(summaries.length, 3);
  assert.equal(d.expandAll().length, 3);
});

test('schema versions stay in separate groups', () => {
  const d = new ErrorDigest();
  d.add(err({ instancePath: [3], schemaVersion: 'v1' }));
  d.add(err({ instancePath: [3], schemaVersion: 'v2' }));
  assert.equal(d.summaries().length, 2);
  assert.equal(d.expandByRule('minimum', 'v1').length, 1);
  assert.equal(d.expandByRule('minimum', 'v2').length, 1);
});

test('multiple errors at the same path are preserved', () => {
  const d = new ErrorDigest();
  d.add(err({ instancePath: [7, 'name'], keyword: 'minLength', message: 'too short' }));
  d.add(err({ instancePath: [7, 'name'], keyword: 'pattern', message: 'bad chars' }));
  d.add(err({ instancePath: [7, 'name'], keyword: 'minLength', message: 'too short' })); // exact duplicate
  assert.equal(d.count, 3);
  const atPath = d.expandByPrefix([7]);
  assert.equal(atPath.length, 3);
  assert.equal(atPath.filter((e) => e.keyword === 'minLength').length, 2);
});

test('online retract splits a range without rebuilding the index', () => {
  const d = new ErrorDigest();
  const ids: number[] = [];
  for (let i = 1; i <= 10; i++) ids.push(d.add(err({ instancePath: [i] })));
  assert.ok(d.remove(ids[4])); // retract item 5
  assert.deepEqual(rangeRuns(d), [
    { start: 1, end: 4, idStart: 0 },
    { start: 6, end: 10, idStart: 5 },
  ]);
  assert.equal(d.count, 9);
  assert.ok(!d.remove(ids[4])); // already gone
  assert.ok(!d.remove(9999)); // unknown id
});

test('expansion by rule, by prefix, and in original insertion order', () => {
  const d = new ErrorDigest();
  d.add(err({ instancePath: [2], keyword: 'minimum' }));
  d.add(err({ instancePath: [0], keyword: 'type', message: 'expected type integer' }));
  d.add(err({ instancePath: [1], keyword: 'minimum' }));
  d.add(err({ instancePath: [2, 'name'], keyword: 'minLength', message: 'too short' }));

  assert.deepEqual(
    d.expandByRule('minimum').map((e) => e.instancePath),
    [[2], [1]], // insertion order, not path order
  );
  assert.deepEqual(
    d.expandByPrefix([2]).map((e) => e.keyword),
    ['minimum', 'minLength'],
  );
  assert.deepEqual(
    d.expandAll('path').map((e) => e.instancePath),
    [[0], [1], [2], [2, 'name']],
  );
  assert.deepEqual(
    d.expandAll('insertion').map((e) => e.instancePath),
    [[2], [0], [1], [2, 'name']],
  );
});

test('paths without numeric segments are stored as literals', () => {
  const d = new ErrorDigest();
  d.add(err({ instancePath: ['meta', 'title'], keyword: 'required', message: 'missing' }));
  const doc = encode(d);
  assert.equal(doc.groups[0].templates[0].kind, 'literal');
  assert.deepEqual(d.expandByPrefix(['meta']).length, 1);
});
