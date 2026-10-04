import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fromCli, fromEnv, fromFile, fromJsonText } from '../src/sources.ts';
import type { ConfigSchema } from '../src/types.ts';

const schema: ConfigSchema = {
  'server.port': { type: 'number' },
  'server.host': { type: 'string' },
  debug: { type: 'boolean' },
  tags: { type: 'string[]' },
};

test('env vars nest via separator and coerce by schema', () => {
  const layer = fromEnv({
    prefix: 'APP_',
    schema,
    env: { APP_SERVER__PORT: '8080', APP_DEBUG: 'yes', OTHER: 'x' },
  });
  assert.deepEqual(layer.value, { server: { port: 8080 }, debug: true });
  assert.deepEqual(layer.issues, []);
});

test('env type conversion failure is located by variable name', () => {
  const layer = fromEnv({ prefix: 'APP_', schema, env: { APP_SERVER__PORT: 'abc' } });
  assert.equal(layer.issues.length, 1);
  const issue = layer.issues[0]!;
  assert.equal(issue.code, 'type-conversion');
  assert.equal(issue.path, 'server.port');
  assert.equal(issue.source!.kind, 'env');
  assert.equal(issue.source!.name, 'APP_SERVER__PORT');
  assert.match(issue.message, /cannot convert 'abc' to number/);
  assert.deepEqual(layer.value, {});
});

test('cli parses --key=value, --key value, --flag and --no-flag', () => {
  const layer = fromCli(
    ['--server.port=9000', '--server.host', 'example.com', '--debug', '--no-verbose'],
    { schema },
  );
  assert.deepEqual(layer.value, {
    server: { port: 9000, host: 'example.com' },
    debug: true,
    verbose: false,
  });
});

test('cli type conversion failure is located by flag and argv index', () => {
  const layer = fromCli(['--debug', '--server.port=xyz'], { schema });
  assert.equal(layer.issues.length, 1);
  const issue = layer.issues[0]!;
  assert.equal(issue.code, 'type-conversion');
  assert.equal(issue.path, 'server.port');
  assert.equal(issue.source!.name, '--server.port');
  assert.equal(issue.source!.location, 'argv[1]');
});

test('cli coerces string[] via schema', () => {
  const layer = fromCli(['--tags=a,b,c'], { schema });
  assert.deepEqual(layer.value, { tags: ['a', 'b', 'c'] });
});

test('values without schema auto-coerce', () => {
  const layer = fromEnv({ env: { A: '1', B: 'true', C: 'text' } });
  assert.deepEqual(layer.value, { a: 1, b: true, c: 'text' });
});

test('duplicate keys in a JSON file are reported with both locations', () => {
  const layer = fromJsonText('{\n  "a": 1,\n  "b": 2,\n  "a": 3\n}', 'dup.json');
  assert.equal(layer.issues.length, 1);
  const issue = layer.issues[0]!;
  assert.equal(issue.code, 'duplicate-key');
  assert.equal(issue.path, 'a');
  assert.equal(issue.source!.location, 'dup.json:4:3');
  assert.match(issue.message, /first defined at dup\.json:2:3/);
  assert.deepEqual(layer.value, { a: 3, b: 2 });
});

test('fromFile reads and parses a real file with locations', () => {
  const dir = mkdtempSync(join(tmpdir(), 'cfg-'));
  const file = join(dir, 'config.json');
  writeFileSync(file, '{\n  "server": {\n    "port": 8080\n  }\n}');
  const layer = fromFile(file);
  assert.deepEqual(layer.value, { server: { port: 8080 } });
  assert.deepEqual(layer.locations!.get('server.port'), { line: 3, column: 5 });
});

test('env/cli values that look like JSON objects pass through (list directives)', () => {
  const env = fromEnv({ env: { APP_TAGS: '{"$append":["prod"]}' }, prefix: 'APP_' });
  assert.deepEqual(env.value, { tags: { $append: ['prod'] } });
  const cli = fromCli(['--tags=["a","b"]']);
  assert.deepEqual(cli.value, { tags: ['a', 'b'] });
});
