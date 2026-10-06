'use strict';

const assert = require('assert');
const os = require('os');
const path = require('path');
const { Builder, BuildCache, CompileError } = require('../src');

let passed = 0;
function test(name, fn) {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    console.error(`FAIL ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

function fullRebuildOutputs(sources) {
  const { outputs, stats } = new Builder({ cache: new BuildCache() }).build(sources);
  assert.strictEqual(stats.compileCount, Object.keys(sources).length, 'full rebuild must compile everything');
  return Object.fromEntries(outputs);
}

function assertMatchesFullRebuild(result, sources) {
  assert.deepStrictEqual(Object.fromEntries(result.outputs), fullRebuildOutputs(sources));
}

function sorted(arr) {
  return arr.slice().sort();
}

// ---------------------------------------------------------------- chain graph
// a -> b -> c, plus independent d
const chain = {
  a: `import { greet } from './b';
export function aMain(): string { return 'a+' + greet('x'); }`,
  b: `import { shout } from './c';
export function greet(name: string): string { return shout(name); }`,
  c: `export function shout(s: string): string { return s.toUpperCase(); }`,
  d: `export const VERSION: number = 1;`,
};

test('initial build compiles every module once', () => {
  const { stats } = new Builder({ cache: new BuildCache() }).build(chain);
  assert.strictEqual(stats.compileCount, 4);
  assert.deepStrictEqual(sorted(stats.compiled), ['a', 'b', 'c', 'd']);
});

test('no-change rebuild reuses cache entirely and matches full rebuild', () => {
  const builder = new Builder({ cache: new BuildCache() });
  builder.build(chain);
  const second = builder.build(chain);
  assert.strictEqual(second.stats.compileCount, 0);
  assert.deepStrictEqual(sorted(second.stats.reused), ['a', 'b', 'c', 'd']);
  assertMatchesFullRebuild(second, chain);
});

test('internal implementation change recompiles only the changed module', () => {
  const builder = new Builder({ cache: new BuildCache() });
  builder.build(chain);
  const changed = {
    ...chain,
    // body changed, export signature (name, params, return type) identical
    c: `export function shout(s: string): string { return (s + '!').toUpperCase().trim(); }`,
  };
  const result = builder.build(changed);
  assert.deepStrictEqual(sorted(result.stats.compiled), ['c']);
  assert.deepStrictEqual(sorted(result.stats.reused), ['a', 'b', 'd']);
  assertMatchesFullRebuild(result, changed);
});

test('export change invalidates direct dependents, cascading while signatures keep changing', () => {
  const builder = new Builder({ cache: new BuildCache() });
  builder.build(chain);

  // c's export changes -> b's key (source + sig(c)) invalidated.
  // b's own export signature is unchanged, so a's key is unaffected.
  const step1 = { ...chain, c: `export function shout(s: string, times: number): string { return s.repeat(times); }` };
  const r1 = builder.build(step1);
  assert.deepStrictEqual(sorted(r1.stats.compiled), ['b', 'c']);
  assert.deepStrictEqual(sorted(r1.stats.reused), ['a', 'd']);
  assertMatchesFullRebuild(r1, step1);

  // now b's export signature also changes -> invalidation propagates to a
  const step2 = { ...step1, b: `import { shout } from './c';\nexport function greet(name: string, times: number): string { return shout(name, times); }` };
  const r2 = builder.build(step2);
  assert.deepStrictEqual(sorted(r2.stats.compiled), ['a', 'b']);
  assert.deepStrictEqual(sorted(r2.stats.reused), ['c', 'd']);
  assertMatchesFullRebuild(r2, step2);
});

test('exported const type change invalidates dependents', () => {
  const builder = new Builder({ cache: new BuildCache() });
  const base = {
    lib: `export const LIMIT: number = 10;`,
    app: `import { LIMIT } from './lib';\nexport function cap(x: number): number { return Math.min(x, LIMIT); }`,
  };
  builder.build(base);
  const changed = { ...base, lib: `export const LIMIT: string = '10';` };
  const result = builder.build(changed);
  assert.deepStrictEqual(sorted(result.stats.compiled), ['app', 'lib']);
  assertMatchesFullRebuild(result, changed);
});

// ---------------------------------------------------------------- failures
test('failed compilation never enters the cache', () => {
  const cache = new BuildCache();
  const builder = new Builder({ cache });
  builder.build(chain);
  const keyOfBBefore = cache.get('b').key;

  const broken = { ...chain, b: `import { shout } from './c';\nexport function greet(name: string): string { return shout(name; }` };
  assert.throws(() => builder.build(broken), CompileError);
  // failing again must re-attempt compilation (no cached failure artifact)
  assert.throws(() => builder.build(broken), CompileError);
  // the previous valid entry for b is untouched by the failed attempt
  assert.strictEqual(cache.get('b').key, keyOfBBefore);

  // fix b with a new implementation (same export signature):
  // only b recompiles; a, c, d are served from the intact cache
  const fixed = { ...chain, b: `import { shout } from './c';\nexport function greet(name: string): string { return shout(name).trim(); }` };
  const result = builder.build(fixed);
  assert.deepStrictEqual(sorted(result.stats.compiled), ['b']);
  assert.deepStrictEqual(sorted(result.stats.reused), ['a', 'c', 'd']);
  assertMatchesFullRebuild(result, fixed);
});

test('failure inside a cyclic group caches none of its members', () => {
  const cache = new BuildCache();
  const builder = new Builder({ cache });
  const cyclic = {
    x: `import { yVal } from './y';\nexport function xVal(): number { return yVal() + 1; }`,
    y: `import { xVal } from './x';\nexport function yVal(): number { return 1; }`,
  };
  builder.build(cyclic);
  const groupKeyBefore = cache.get('x|y').key;

  const broken = { ...cyclic, y: `import { xVal } from './x';\nexport function yVal(): number { return (1; }` };
  assert.throws(() => builder.build(broken), CompileError);
  // group entry not overwritten by the failed attempt
  assert.strictEqual(cache.get('x|y').key, groupKeyBefore);

  // fix y with a new implementation: the whole SCC recompiles as a unit
  const fixed = { ...cyclic, y: `import { xVal } from './x';\nexport function yVal(): number { return 2; }` };
  const result = builder.build(fixed);
  assert.deepStrictEqual(sorted(result.stats.compiled), ['x', 'y']);
  assertMatchesFullRebuild(result, fixed);
});

// ---------------------------------------------------------------- cycles
// x <-> y (cycle), z depends on the cycle via y
const cyclic = {
  x: `import { yVal } from './y';\nexport function xVal(): number { return yVal() + 1; }`,
  y: `import { xVal } from './x';\nexport function yVal(): number { return 1; }`,
  z: `import { yVal } from './y';\nexport function zVal(): number { return yVal() * 2; }`,
};

test('cyclic dependencies compile as one strongly connected group', () => {
  const { stats } = new Builder({ cache: new BuildCache() }).build(cyclic);
  assert.strictEqual(stats.compileCount, 3);
});

test('internal change inside a cycle recompiles the group but not dependents', () => {
  const builder = new Builder({ cache: new BuildCache() });
  builder.build(cyclic);
  const changed = {
    ...cyclic,
    y: `import { xVal } from './x';\nexport function yVal(): number { return 40 + 2; }`,
  };
  const result = builder.build(changed);
  assert.deepStrictEqual(sorted(result.stats.compiled), ['x', 'y']); // whole SCC
  assert.deepStrictEqual(sorted(result.stats.reused), ['z']); // group exports unchanged
  assertMatchesFullRebuild(result, changed);
});

test('export change inside a cycle invalidates the group and its dependents', () => {
  const builder = new Builder({ cache: new BuildCache() });
  builder.build(cyclic);
  const changed = {
    ...cyclic,
    y: `import { xVal } from './x';\nexport function yVal(fallback: number): number { return fallback; }`,
  };
  const result = builder.build(changed);
  assert.deepStrictEqual(sorted(result.stats.compiled), ['x', 'y', 'z']);
  assertMatchesFullRebuild(result, changed);
});

// ---------------------------------------------------------------- diamond
test('diamond graph: invalidation counts are exact at every level', () => {
  const diamond = {
    top: `import { f } from './left';\nimport { g } from './right';\nexport function top(): number { return f() + g(); }`,
    left: `import { base } from './base';\nexport function f(): number { return base() + 1; }`,
    right: `import { base } from './base';\nexport function g(): number { return base() + 2; }`,
    base: `export function base(): number { return 1; }`,
  };
  const builder = new Builder({ cache: new BuildCache() });
  builder.build(diamond);

  // internal change at the bottom: only base recompiles
  const implChange = { ...diamond, base: `export function base(): number { return 99; }` };
  const r1 = builder.build(implChange);
  assert.deepStrictEqual(sorted(r1.stats.compiled), ['base']);
  assertMatchesFullRebuild(r1, implChange);

  // export change at the bottom: direct dependents recompile;
  // left/right signatures are unchanged, so top stays cached
  const sigChange = { ...implChange, base: `export function base(step: number): number { return step; }` };
  const r2 = builder.build(sigChange);
  assert.deepStrictEqual(sorted(r2.stats.compiled), ['base', 'left', 'right']);
  assert.deepStrictEqual(sorted(r2.stats.reused), ['top']);
  assertMatchesFullRebuild(r2, sigChange);

  // export change at the middle level propagates further up
  const midChange = { ...sigChange, left: `import { base } from './base';\nexport function f(step: number): number { return base(step) + 1; }` };
  const r3 = builder.build(midChange);
  assert.deepStrictEqual(sorted(r3.stats.compiled), ['left', 'top']);
  assert.deepStrictEqual(sorted(r3.stats.reused), ['base', 'right']);
  assertMatchesFullRebuild(r3, midChange);
});

// ---------------------------------------------------------------- persistence
test('cache persists to disk and survives reload', () => {
  const file = path.join(os.tmpdir(), `tsbuild-cache-${process.pid}.json`);
  const cache = new BuildCache();
  const builder = new Builder({ cache });
  builder.build(chain);
  cache.save(file);

  const reloaded = new Builder({ cache: BuildCache.load(file) });
  const result = reloaded.build(chain);
  assert.strictEqual(result.stats.compileCount, 0);
  assertMatchesFullRebuild(result, chain);
});

console.log(`\n${passed} tests passed`);
