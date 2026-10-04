import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fromDefaults, fromJsonText } from '../src/sources.ts';
import { mergeLayers } from '../src/merge.ts';
import { provenanceAt, explain } from '../src/index.ts';

test('precedence: cli > env > file > defaults', () => {
  const result = mergeLayers([
    fromDefaults({ a: 1, b: 1 }),
    { kind: 'file', name: 'c.json', value: { a: 2 } },
    { kind: 'env', name: 'env', value: { a: 3 } },
    { kind: 'cli', name: 'cli', value: { a: 4 } },
  ]);
  assert.deepEqual(result.value, { a: 4, b: 1 });
});

test('objects merge deeply, scalars replace', () => {
  const result = mergeLayers([
    fromDefaults({ server: { host: 'localhost', port: 80 }, debug: false }),
    fromJsonText('{"server": {"port": 8080}}', 'app.json'),
  ]);
  assert.deepEqual(result.value, { server: { host: 'localhost', port: 8080 }, debug: false });
});

test('plain array in higher layer replaces lower array', () => {
  const result = mergeLayers([
    fromDefaults({ tags: ['a', 'b'] }),
    fromJsonText('{"tags": ["c"]}', 'app.json'),
  ]);
  assert.deepEqual(result.value, { tags: ['c'] });
});

test('$append keeps base items and appends', () => {
  const result = mergeLayers([
    fromDefaults({ tags: ['a', 'b'] }),
    fromJsonText('{"tags": {"$append": ["c"]}}', 'app.json'),
  ]);
  assert.deepEqual(result.value, { tags: ['a', 'b', 'c'] });
});

test('$remove deletes specified values', () => {
  const result = mergeLayers([
    fromDefaults({ tags: ['a', 'b', 'c'] }),
    fromJsonText('{"tags": {"$remove": ["b"]}}', 'app.json'),
  ]);
  assert.deepEqual(result.value, { tags: ['a', 'c'] });
});

test('$replace discards base list; directives compose', () => {
  const result = mergeLayers([
    fromDefaults({ tags: ['a', 'b'] }),
    fromJsonText('{"tags": {"$replace": ["x", "y"]}}', 'one.json'),
    fromJsonText('{"tags": {"$remove": ["x"], "$append": ["z"]}}', 'two.json'),
  ]);
  assert.deepEqual(result.value, { tags: ['y', 'z'] });
});

test('list directive on non-array base reports invalid-list-op', () => {
  const result = mergeLayers([
    fromDefaults({ tags: 'not-a-list' }),
    fromJsonText('{"tags": {"$append": ["c"]}}', 'app.json'),
  ]);
  assert.equal(result.issues.length, 1);
  assert.equal(result.issues[0]!.code, 'invalid-list-op');
  assert.equal(result.issues[0]!.path, 'tags');
});

test('provenance keeps winning source and overridden chain', () => {
  const result = mergeLayers([
    fromDefaults({ port: 80 }),
    fromJsonText('{"port": 8080}', 'base.json'),
    fromJsonText('{"port": 9090}', 'override.json'),
  ]);
  const info = provenanceAt(result.provenance, 'port')!;
  assert.equal(info.value, 9090);
  assert.equal(info.source.kind, 'file');
  assert.equal(info.source.name, 'override.json');
  assert.equal(info.overridden.length, 2);
  assert.equal(info.overridden[0]!.value, 8080);
  assert.equal(info.overridden[1]!.value, 80);
  assert.equal(info.overridden[1]!.source.kind, 'defaults');
});

test('provenance tracks individual array elements after $append', () => {
  const result = mergeLayers([
    fromDefaults({ tags: ['a'] }),
    fromJsonText('{"tags": {"$append": ["b"]}}', 'app.json'),
  ]);
  assert.equal(provenanceAt(result.provenance, 'tags[0]')!.source.kind, 'defaults');
  assert.equal(provenanceAt(result.provenance, 'tags[1]')!.source.kind, 'file');
});

test('file provenance carries line/column locations', () => {
  const result = mergeLayers([fromJsonText('{\n  "port": 8080\n}', 'app.json')]);
  const info = provenanceAt(result.provenance, 'port')!;
  assert.equal(info.source.location, 'app.json:2:3');
});

test('explain renders the overridden chain', () => {
  const result = mergeLayers([
    fromDefaults({ port: 80 }),
    fromJsonText('{"port": 8080}', 'app.json'),
  ]);
  const text = explain(result.provenance, 'port');
  assert.match(text, /value=8080 from file:app\.json/);
  assert.match(text, /overridden=80 from defaults:defaults/);
});
