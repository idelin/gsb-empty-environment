import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fromDefaults, fromJsonText } from '../src/sources.ts';
import { mergeLayers } from '../src/merge.ts';
import { validate } from '../src/validate.ts';
import { loadConfig } from '../src/index.ts';
import type { ConfigSchema } from '../src/types.ts';

test('null values are reported with path and originating source location', () => {
  const merged = mergeLayers([
    fromDefaults({ a: 1 }),
    fromJsonText('{\n  "a": null,\n  "b": {"c": null}\n}', 'app.json'),
  ]);
  const issues = validate(merged);
  assert.equal(issues.length, 2);
  const [a, c] = issues;
  assert.equal(a!.code, 'null-value');
  assert.equal(a!.path, 'a');
  assert.equal(a!.source!.location, 'app.json:2:3');
  assert.equal(c!.path, 'b.c');
  assert.equal(c!.source!.location, 'app.json:3:9');
});

test('nullable schema fields suppress null reports', () => {
  const merged = mergeLayers([fromJsonText('{"a": null}', 'app.json')]);
  const issues = validate(merged, { a: { type: 'number', nullable: true } });
  assert.deepEqual(issues, []);
});

test('schema type mismatches are reported on final values', () => {
  const merged = mergeLayers([fromJsonText('{"port": "8080"}', 'app.json')]);
  const issues = validate(merged, { port: { type: 'number' } });
  assert.equal(issues.length, 1);
  assert.equal(issues[0]!.code, 'type-mismatch');
  assert.match(issues[0]!.message, /expected number at 'port', got string/);
});

test('loadConfig aggregates issues from every stage', () => {
  const schema: ConfigSchema = { port: { type: 'number' } };
  const loaded = loadConfig({
    defaults: { port: 80, host: null },
    env: { prefix: 'APP_', schema, env: { APP_PORT: 'not-a-number' } },
    schema,
  });
  const codes = loaded.issues.map((i) => i.code).sort();
  assert.deepEqual(codes, ['null-value', 'type-conversion']);
  const conv = loaded.issues.find((i) => i.code === 'type-conversion')!;
  assert.equal(conv.source!.name, 'APP_PORT');
  assert.equal(loaded.value as any && (loaded.value as any).port, 80);
});
