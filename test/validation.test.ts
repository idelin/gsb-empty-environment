import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { ConfigError } from '../src/errors.ts';
import { mergeConfig } from '../src/merge.ts';
import { fromArgv, fromDefaults, fromEnv, fromFile } from '../src/sources.ts';
import { capture } from './helpers.ts';

test('null values are rejected with path and source location', () => {
  const dir = mkdtempSync(join(tmpdir(), 'cfg-'));
  const file = join(dir, 'config.json');
  writeFileSync(file, '{\n  "db": {\n    "host": null\n  }\n}');
  const err = capture(
    () => mergeConfig(fromFile(file), { schema: { db: { host: 'string' } } }),
    ConfigError,
  );
  assert.equal(err.code, 'NULL_VALUE');
  assert.equal(err.path, 'db.host');
  assert.equal(err.source?.kind, 'file');
  assert.deepEqual(err.source?.location, { line: 3, column: 5 });
  assert.match(err.message, /config\.json:3:5/);
});

test('nullable keys accept explicit null', () => {
  const config = mergeConfig(fromDefaults({ host: null }), {
    schema: { host: 'string|null' },
  });
  assert.equal(config.get('host'), null);
});

test('type conversion failure from env reports var name and raw value', () => {
  const err = capture(
    () => mergeConfig(fromEnv({ APP_PORT: 'not-a-number' }, { prefix: 'APP_' }), { schema: { port: 'number' } }),
    ConfigError,
  );
  assert.equal(err.code, 'TYPE_CONVERSION');
  assert.equal(err.path, 'port');
  assert.deepEqual(err.source, { kind: 'env', name: 'APP_PORT' });
  assert.deepEqual(err.details, { raw: 'not-a-number', expected: 'number' });
});

test('type conversion failure from argv reports the offending flag', () => {
  const err = capture(
    () => mergeConfig(fromArgv(['--debug=maybe']), { schema: { debug: 'boolean' } }),
    ConfigError,
  );
  assert.equal(err.code, 'TYPE_CONVERSION');
  assert.deepEqual(err.source, { kind: 'argv', name: '--debug=maybe' });
});

test('env/argv strings convert to schema types', () => {
  const config = mergeConfig(
    [
      ...fromEnv({ APP_PORT: '8080', APP_DEBUG: 'yes', APP_RATIO: '0.5' }, { prefix: 'APP_' }),
      ...fromArgv(['--name=svc']),
    ],
    { schema: { port: 'number', debug: 'boolean', ratio: 'number', name: 'string' } },
  );
  assert.equal(config.get('port'), 8080);
  assert.equal(config.get('debug'), true);
  assert.equal(config.get('ratio'), 0.5);
  assert.equal(config.get('name'), 'svc');
});

test('unknown keys are rejected when a schema is present', () => {
  const err = capture(
    () => mergeConfig(fromArgv(['--typo=1']), { schema: { port: 'number' } }),
    ConfigError,
  );
  assert.equal(err.code, 'UNKNOWN_KEY');
  assert.equal(err.path, 'typo');
});

test('list ops on non-array keys are rejected', () => {
  const err = capture(
    () => mergeConfig(fromArgv(['--port+=1']), { schema: { port: 'number' } }),
    ConfigError,
  );
  assert.equal(err.code, 'INVALID_LIST_OP');
});

test('duplicate keys in a config file abort loading with position', () => {
  const dir = mkdtempSync(join(tmpdir(), 'cfg-'));
  const file = join(dir, 'config.json');
  writeFileSync(file, '{\n  "a": 1,\n  "a": 2\n}');
  const err = capture(() => fromFile(file), ConfigError);
  assert.equal(err.code, 'DUPLICATE_KEY');
  assert.match(err.message, /:3:3/);
});
