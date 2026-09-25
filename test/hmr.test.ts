import assert from 'node:assert/strict';
import test from 'node:test';
import { Registry } from '../src/linker.ts';
import type { ModuleSpec } from '../src/types.ts';

function svcSpec(version: string, init?: () => Record<string, unknown>): ModuleSpec {
  return {
    name: 'svc',
    version,
    exports: { fetch: { kind: 'value', signature: '() => Promise<string>' } },
    imports: [],
    init: init ?? (() => ({ fetch: () => Promise.resolve(`data-${version}`) })),
  };
}

test('init failure rolls back the whole batch: no partial version swap', () => {
  const registry = new Registry();
  registry.register(svcSpec('1'));
  const first = registry.hmrReplace([{ name: 'svc', version: '1' }]);
  assert.equal(first.ok, true);

  registry.register(svcSpec('2', () => { throw new Error('boom'); }));
  const failed = registry.hmrReplace([{ name: 'svc', version: '2' }]);
  assert.equal(failed.ok, false);
  if (failed.ok) return;
  assert.equal(failed.rolledBack, true);
  assert.equal(failed.errors[0].code, 'INIT_FAILED');
  assert.match(failed.errors[0].message, /boom/);
  assert.equal(failed.snapshot.live.svc, '1', 'snapshot recorded the pre-decision live version');

  const fetch = registry.getExport('svc', 'fetch') as () => Promise<string>;
  return fetch().then((value) => {
    assert.equal(value, 'data-1', 'old version keeps serving after failed swap');
    assert.equal(registry.cacheSize, 1, 'failed batch left no partial cache entry');
  });
});

test('old async property still resolves the old value after hot replacement', async () => {
  const registry = new Registry();
  registry.register(svcSpec('1'));
  registry.register(svcSpec('2'));

  assert.equal(registry.hmrReplace([{ name: 'svc', version: '1' }]).ok, true);
  const staleFetch = registry.getExport('svc', 'fetch') as () => Promise<string>;

  const swap = registry.hmrReplace([{ name: 'svc', version: '2' }]);
  assert.equal(swap.ok, true);
  if (!swap.ok) return;
  assert.equal(swap.previous.live.svc, '1');
  assert.equal(swap.current.live.svc, '2');

  assert.equal(await staleFetch(), 'data-1', 'closure captured before swap keeps old version alive');
  const liveFetch = registry.getExport('svc', 'fetch') as () => Promise<string>;
  assert.equal(await liveFetch(), 'data-2');
  assert.equal(registry.cacheSize, 2, 'old and new version vectors stay isolated in cache');
});

test('failed HMR validation keeps the live batch untouched', () => {
  const registry = new Registry();
  registry.register(svcSpec('1'));
  assert.equal(registry.hmrReplace([{ name: 'svc', version: '1' }]).ok, true);

  // v2 imports a missing module: validation must fail before anything initializes.
  registry.register({
    name: 'svc',
    version: '2',
    exports: { fetch: { kind: 'value', signature: '() => Promise<string>' } },
    imports: [{ from: 'ghost', version: '1', names: { x: '() => void' } }],
    init: () => ({ fetch: () => Promise.resolve('data-2') }),
  });
  const result = registry.hmrReplace([{ name: 'svc', version: '2' }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.errors[0].code, 'MODULE_NOT_FOUND');
  const fetch = registry.getExport('svc', 'fetch') as () => Promise<string>;
  return fetch().then((value) => assert.equal(value, 'data-1'));
});
