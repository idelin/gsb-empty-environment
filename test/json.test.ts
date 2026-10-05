import assert from 'node:assert/strict';
import test from 'node:test';
import { ConfigError } from '../src/errors.ts';
import { parseJson } from '../src/json.ts';
import { capture } from './helpers.ts';

test('parses values and records key locations', () => {
  const { value, locations } = parseJson('{\n  "a": 1,\n  "b": { "c": true }\n}', 'cfg.json');
  assert.deepEqual(value, { a: 1, b: { c: true } });
  assert.deepEqual(locations.get('a'), { line: 2, column: 3 });
  assert.deepEqual(locations.get('b.c'), { line: 3, column: 10 });
});

test('rejects duplicate keys with line/column of both occurrences', () => {
  const text = '{\n  "port": 80,\n  "host": "x",\n  "port": 81\n}';
  const err = capture(() => parseJson(text, 'cfg.json'), ConfigError);
  assert.equal(err.code, 'DUPLICATE_KEY');
  assert.equal(err.path, 'port');
  assert.deepEqual(err.source?.location, { line: 4, column: 3 });
  assert.match(err.message, /line 2, column 3/);
  assert.match(err.message, /cfg\.json:4:3/);
});

test('rejects duplicate keys in nested objects', () => {
  const err = capture(
    () => parseJson('{"db": {"host": "a", "host": "b"}}', 'cfg.json'),
    ConfigError,
  );
  assert.equal(err.code, 'DUPLICATE_KEY');
  assert.equal(err.path, 'db.host');
});

test('reports syntax errors with position', () => {
  const err = capture(() => parseJson('{"a": }', 'cfg.json'), ConfigError);
  assert.equal(err.code, 'PARSE_ERROR');
  assert.deepEqual(err.source?.location, { line: 1, column: 7 });
});
