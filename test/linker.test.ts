import assert from 'node:assert/strict';
import test from 'node:test';
import { Registry, shortestCycle } from '../src/linker.ts';
import type { ModuleSpec } from '../src/types.ts';
import { vectorKey } from '../src/types.ts';

function makeSpec(partial: Partial<ModuleSpec> & { name: string; version: string }): ModuleSpec {
  return { exports: {}, imports: [], ...partial };
}

test('diamond shared import: shared dependency initializes exactly once', () => {
  const registry = new Registry();
  let dInits = 0;
  registry.register(makeSpec({
    name: 'd', version: '1',
    exports: { who: { kind: 'value', signature: '() => string' } },
    init: () => { dInits += 1; return { who: () => 'd@1' }; },
  }));
  for (const mid of ['b', 'c']) {
    registry.register(makeSpec({
      name: mid, version: '1',
      exports: { [`whoFrom${mid}`]: { kind: 'value', signature: '() => string' } },
      imports: [{ from: 'd', version: '1', names: { who: '() => string' } }],
      init: (ns) => ({ [`whoFrom${mid}`]: (ns.d as { who: () => string }).who }),
    }));
  }
  registry.register(makeSpec({
    name: 'a', version: '1',
    exports: { report: { kind: 'value', signature: '() => string' } },
    imports: [
      { from: 'b', version: '1', names: { whoFromb: '() => string' } },
      { from: 'c', version: '1', names: { whoFromc: '() => string' } },
    ],
    init: (ns) => ({
      report: () =>
        `${(ns.b as { whoFromb: () => string }).whoFromb()}+${(ns.c as { whoFromc: () => string }).whoFromc()}`,
    }),
  }));

  const result = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(result.ok, true);
  if (!result.ok) return;
  assert.equal(dInits, 1, 'd must initialize exactly once across the diamond');
  const report = result.records.get('a')!.exports.report as () => string;
  assert.equal(report(), 'd@1+d@1', 'b and c observe the same shared d instance');
});

test('same-name shadowing: namespaces isolate identical export names', () => {
  const registry = new Registry();
  registry.register(makeSpec({
    name: 'x', version: '1',
    exports: { util: { kind: 'value', signature: '() => string' } },
    init: () => ({ util: () => 'util-from-x' }),
  }));
  registry.register(makeSpec({
    name: 'y', version: '1',
    exports: { util: { kind: 'value', signature: '() => string' } },
    init: () => ({ util: () => 'util-from-y' }),
  }));
  registry.register(makeSpec({
    name: 'app', version: '1',
    exports: { both: { kind: 'value', signature: '() => string' } },
    imports: [
      { from: 'x', version: '1', names: { util: '() => string' } },
      { from: 'y', version: '1', names: { util: '() => string' } },
    ],
    init: (ns) => ({
      both: () => `${(ns.x as { util: () => string }).util()}|${(ns.y as { util: () => string }).util()}`,
    }),
  }));

  const result = registry.link([{ name: 'app', version: '1' }]);
  assert.equal(result.ok, true);
  if (!result.ok) return;
  const both = result.records.get('app')!.exports.both as () => string;
  assert.equal(both(), 'util-from-x|util-from-y');
});

test('version must match exactly', () => {
  const registry = new Registry();
  registry.register(makeSpec({ name: 'b', version: '1' }));
  registry.register(makeSpec({
    name: 'a', version: '1',
    imports: [{ from: 'b', version: '2', names: {} }],
  }));
  const result = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.errors[0].code, 'VERSION_MISMATCH');
  assert.match(result.errors[0].message, /'b' required at version '2'/);
});

test('declaration cycle is allowed (no evaluation side effects)', () => {
  const registry = new Registry();
  registry.register(makeSpec({
    name: 'a', version: '1',
    exports: { AName: { kind: 'decl', signature: '() => string' } },
    declare: { AName: () => 'A' },
    imports: [{ from: 'b', version: '1', names: { BName: '() => string' } }],
  }));
  registry.register(makeSpec({
    name: 'b', version: '1',
    exports: { BName: { kind: 'decl', signature: '() => string' } },
    declare: { BName: () => 'B' },
    imports: [{ from: 'a', version: '1', names: { AName: '() => string' } }],
  }));
  const result = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(result.ok, true, result.ok ? '' : result.errors[0].message);
});

test('evaluation cycle is rejected with the shortest error source chain', () => {
  const registry = new Registry();
  // Cycles: a->b->a (length 2) and b->c->d->b (length 3). Shortest must be reported.
  const value = { kind: 'value', signature: '() => void' } as const;
  registry.register(makeSpec({
    name: 'a', version: '1', exports: { v: value },
    imports: [{ from: 'b', version: '1', names: { v: '() => void' } }],
    init: () => ({ v: () => {} }),
  }));
  registry.register(makeSpec({
    name: 'b', version: '1', exports: { v: value },
    imports: [
      { from: 'a', version: '1', names: { v: '() => void' } },
      { from: 'c', version: '1', names: { v: '() => void' } },
    ],
    init: () => ({ v: () => {} }),
  }));
  registry.register(makeSpec({
    name: 'c', version: '1', exports: { v: value },
    imports: [{ from: 'd', version: '1', names: { v: '() => void' } }],
    init: () => ({ v: () => {} }),
  }));
  registry.register(makeSpec({
    name: 'd', version: '1', exports: { v: value },
    imports: [{ from: 'b', version: '1', names: { v: '() => void' } }],
    init: () => ({ v: () => {} }),
  }));
  const result = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.errors[0].code, 'EVAL_CYCLE');
  assert.match(result.errors[0].message, /a -> b -> a/, 'reports the shortest cycle chain');
});

test('shortestCycle picks the minimal chain among several cycles', () => {
  const graph = new Map<string, Set<string>>([
    ['a', new Set(['b'])],
    ['b', new Set(['a', 'c'])],
    ['c', new Set(['d'])],
    ['d', new Set(['b'])],
  ]);
  assert.deepEqual(shortestCycle(graph), ['a', 'b', 'a']);
});

test('missing export error locates the import statement', () => {
  const registry = new Registry();
  registry.register(makeSpec({
    name: 'b', version: '1',
    exports: { real: { kind: 'value', signature: '() => void' } },
    init: () => ({ real: () => {} }),
  }));
  registry.register(makeSpec({
    name: 'a', version: '1',
    imports: [
      { from: 'b', version: '1', names: { real: '() => void' } },
      { from: 'b', version: '1', names: { nope: '() => void' } },
    ],
  }));
  const result = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  const err = result.errors[0];
  assert.equal(err.code, 'MISSING_EXPORT');
  assert.match(err.message, /module 'a' import statement #1 \(from 'b@1'\): export 'nope' not found/);
  const detail = err.detail as { module: string; importIndex: number; from: string; name: string };
  assert.deepEqual(
    { module: detail.module, importIndex: detail.importIndex, from: detail.from, name: detail.name },
    { module: 'a', importIndex: 1, from: 'b', name: 'nope' },
  );
});

test('signature mismatch is reported before linking', () => {
  const registry = new Registry();
  registry.register(makeSpec({
    name: 'b', version: '1',
    exports: { f: { kind: 'value', signature: '(number) => string' } },
    init: () => ({ f: (n: number) => String(n) }),
  }));
  registry.register(makeSpec({
    name: 'a', version: '1',
    imports: [{ from: 'b', version: '1', names: { f: '(string) => string' } }],
  }));
  const result = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(result.ok, false);
  if (result.ok) return;
  assert.equal(result.errors[0].code, 'SIGNATURE_MISMATCH');
});

test('cache is isolated by the full dependency version vector', () => {
  const registry = new Registry();
  let dInits = 0;
  const makeD = (version: string): ModuleSpec => makeSpec({
    name: 'd', version,
    exports: { who: { kind: 'value', signature: '() => string' } },
    init: () => { dInits += 1; return { who: () => `d@${version}` }; },
  });
  const makeA = (version: string, dVersion: string): ModuleSpec => makeSpec({
    name: 'a', version,
    exports: { who: { kind: 'value', signature: '() => string' } },
    imports: [{ from: 'd', version: dVersion, names: { who: '() => string' } }],
    init: (ns) => ({ who: (ns.d as { who: () => string }).who }),
  });
  registry.register(makeD('1'));
  registry.register(makeA('1', '1'));
  registry.register(makeD('2'));
  registry.register(makeA('2', '2'));

  const first = registry.link([{ name: 'a', version: '1' }]);
  const second = registry.link([{ name: 'a', version: '2' }]);
  assert.equal(first.ok, true);
  assert.equal(second.ok, true);
  if (!first.ok || !second.ok) return;

  assert.notEqual(first.cacheKey, second.cacheKey);
  assert.equal(first.cacheKey, vectorKey({ a: '1', d: '1' }));
  assert.equal(second.cacheKey, vectorKey({ a: '2', d: '2' }));
  assert.equal(registry.cacheSize, 2, 'both version vectors coexist, no cross-version mixing');
  assert.equal(dInits, 2, 'each vector initializes its own d instance');
  assert.notEqual(
    (first.records.get('d')!.exports.who as () => string)(),
    (second.records.get('d')!.exports.who as () => string)(),
  );

  const again = registry.link([{ name: 'a', version: '1' }]);
  assert.equal(again.ok, true);
  if (!again.ok) return;
  assert.equal(again.fromCache, true);
  assert.equal(dInits, 2, 'cached vector is reused without re-initialization');
});
