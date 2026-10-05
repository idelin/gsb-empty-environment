import assert from 'node:assert/strict';
import test from 'node:test';
import { loadConfig } from '../src/index.ts';
import { mergeConfig } from '../src/merge.ts';
import { fromArgv, fromDefaults, fromEnv } from '../src/sources.ts';

test('priority order: defaults < env < argv', () => {
  const config = mergeConfig(
    [
      ...fromDefaults({ port: 80, host: 'localhost' }),
      ...fromEnv({ APP_PORT: '8080' }, { prefix: 'APP_' }),
      ...fromArgv(['--port', '9090']),
    ],
    { schema: { port: 'number', host: 'string' } },
  );
  assert.equal(config.get('port'), 9090);
  assert.equal(config.get('host'), 'localhost');
});

test('provenance keeps the winning source and the override chain', () => {
  const config = mergeConfig([
    ...fromDefaults({ port: 80 }),
    ...fromEnv({ APP_PORT: '8080' }, { prefix: 'APP_' }),
    ...fromArgv(['--port=9090']),
  ]);
  const info = config.explain('port');
  assert.equal(info?.kind, 'leaf');
  assert.deepEqual(info?.source, { kind: 'argv', name: '--port=9090' });
  assert.deepEqual(
    info?.kind === 'leaf' && info.history.map((h) => [h.value, h.source.kind, h.source.name]),
    [
      [80, 'defaults', 'defaults'],
      ['8080', 'env', 'APP_PORT'],
    ],
  );
});

test('list append accumulates across sources with per-element provenance', () => {
  const config = mergeConfig([
    ...fromDefaults({ tags: ['base'] }),
    ...fromDefaults({ 'tags+': ['extra'] }),
    ...fromArgv(['--tags+=cli1,cli2']),
  ]);
  assert.deepEqual(config.get('tags'), ['base', 'extra', 'cli1', 'cli2']);
  const info = config.explain('tags');
  assert.equal(info?.kind, 'array');
  if (info?.kind !== 'array') return;
  assert.deepEqual(
    info.elements.map((el) => [el.value, el.source.kind]),
    [
      ['base', 'defaults'],
      ['extra', 'defaults'],
      ['cli1', 'argv'],
      ['cli2', 'argv'],
    ],
  );
});

test('list replace resets elements and records history', () => {
  const config = mergeConfig([
    ...fromDefaults({ tags: ['a', 'b'] }),
    ...fromDefaults({ 'tags+': ['c'] }),
    ...fromDefaults({ 'tags!': ['x'] }),
  ]);
  assert.deepEqual(config.get('tags'), ['x']);
  const info = config.explain('tags');
  if (info?.kind !== 'array') assert.fail('expected array');
  assert.equal(info.history.length, 1);
  assert.deepEqual(
    info.history[0].elements.map((el) => el.value),
    ['a', 'b', 'c'],
  );
  assert.deepEqual(
    info.events.map((e) => e.op),
    ['set', 'append', 'set'],
  );
});

test('list remove deletes matching values and records what was removed', () => {
  const config = mergeConfig([
    ...fromDefaults({ tags: ['a', 'b', 'c'] }),
    ...fromArgv(['--tags-=b,missing']),
  ]);
  assert.deepEqual(config.get('tags'), ['a', 'c']);
  const info = config.explain('tags');
  if (info?.kind !== 'array') assert.fail('expected array');
  const removeEvent = info.events.find((e) => e.op === 'remove');
  assert.deepEqual(removeEvent?.removed, ['b']);
});

test('nested objects merge deeply across sources', () => {
  const config = mergeConfig([
    ...fromDefaults({ db: { host: 'localhost', port: 5432 } }),
    ...fromEnv({ APP_DB__HOST: 'db.internal' }, { prefix: 'APP_' }),
  ]);
  assert.deepEqual(config.get('db'), { host: 'db.internal', port: 5432 });
});

test('loadConfig wires all four sources in order', () => {
  const config = loadConfig({
    defaults: { port: 80, debug: false },
    env: { APP_PORT: '8080' },
    envOptions: { prefix: 'APP_' },
    argv: ['--no-debug'],
    schema: { port: 'number', debug: 'boolean' },
  });
  assert.equal(config.get('port'), 8080);
  assert.equal(config.get('debug'), false);
});

test('explainText renders a masked provenance report', () => {
  const config = mergeConfig(
    [...fromDefaults({ port: 80 }), ...fromArgv(['--port=9090'])],
    { schema: { port: 'number' } },
  );
  const text = config.explainText('port');
  assert.match(text, /9090.*argv:--port=9090/);
  assert.match(text, /overridden: 80.*defaults/);
});
