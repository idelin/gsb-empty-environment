import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createPlan } from '../src/planner.ts';
import { PluginLoader } from '../src/loader.ts';
import type { PluginContext, PluginModule } from '../src/loader.ts';
import type { PluginManifest, RootRequest } from '../src/manifest.ts';

const m = (name: string, version: string, extra: Partial<PluginManifest> = {}): PluginManifest => ({
  name,
  version,
  ...extra,
});

const makeLoader = (
  registry: PluginManifest[],
  roots: RootRequest[],
  modules: Record<string, PluginModule>,
): PluginLoader => new PluginLoader(createPlan(registry, roots), modules);

test('初始化顺序遵循依赖拓扑', async () => {
  const events: string[] = [];
  const mod = (name: string): PluginModule => ({ init: () => void events.push(`init:${name}`) });
  const loader = makeLoader(
    [
      m('a', '1.0.0', { requires: { b: '^1.0.0' } }),
      m('b', '1.0.0', { requires: { c: '^1.0.0' } }),
      m('c', '1.0.0'),
    ],
    [{ name: 'a' }],
    { a: mod('a'), b: mod('b'), c: mod('c') },
  );
  const report = await loader.initAll();
  assert.deepEqual(events, ['init:c', 'init:b', 'init:a']);
  assert.deepEqual(report.initialized, ['c', 'b', 'a']);
  assert.deepEqual(report.failed, []);
});

test('初始化回调动态请求插件不会重复初始化', async () => {
  let sharedInit = 0;
  let lateInit = 0;
  const seen: unknown[] = [];
  const modules: Record<string, PluginModule> = {
    shared: {
      init: () => {
        sharedInit++;
        return { id: 'shared' };
      },
    },
    late: {
      init: () => {
        lateInit++;
        return { id: 'late' };
      },
    },
    consumer: {
      init: async (ctx: PluginContext) => {
        const first = await ctx.require('shared'); // 已初始化 → 直接复用
        const late1 = await ctx.require('late'); // 未初始化 → 触发一次
        const [a, b] = await Promise.all([ctx.require('late'), ctx.require('late')]); // 并发 → 仍一次
        const again = await ctx.require('late');
        seen.push(first, late1, a, b, again, ctx.get('late'));
      },
    },
  };
  const loader = makeLoader(
    [m('shared', '1.0.0'), m('consumer', '1.0.0'), m('late', '1.0.0')],
    [{ name: 'shared' }, { name: 'consumer' }, { name: 'late' }],
    modules,
  );
  const report = await loader.initAll();
  assert.equal(sharedInit, 1);
  assert.equal(lateInit, 1);
  assert.equal(report.failed.length, 0);
  assert.ok(seen.every((v) => v === seen[0] || v === seen[1]));
  assert.deepEqual([...new Set(seen.map((v) => (v as { id: string }).id))].sort(), ['late', 'shared']);
  // late 在 consumer 的回调里被初始化，主循环不再重复执行
  assert.equal(loader.getState('late'), 'initialized');
});

test('循环动态请求被拒绝且不重复初始化', async () => {
  let xInit = 0;
  let yInit = 0;
  const modules: Record<string, PluginModule> = {
    x: {
      init: async (ctx: PluginContext) => {
        xInit++;
        await ctx.require('y');
      },
    },
    y: {
      init: async (ctx: PluginContext) => {
        yInit++;
        await ctx.require('x');
      },
    },
  };
  const loader = makeLoader([m('x', '1.0.0'), m('y', '1.0.0')], [{ name: 'x' }, { name: 'y' }], modules);
  const report = await loader.initAll();
  assert.equal(xInit, 1);
  assert.equal(yInit, 1);
  // x 初始化失败；y 通过动态依赖边被识别为 x 的依赖方而卸载，且不会重新初始化
  assert.deepEqual(
    report.failed.map((f) => f.name),
    ['x'],
  );
  assert.deepEqual(
    report.unloaded.map((u) => u.name),
    ['y'],
  );
  assert.ok(String((report.failed[0].error as Error).message).includes('循环动态请求'));
});

test('初始化失败只卸载依赖它的已加载部分，独立插件保留', async () => {
  const events: string[] = [];
  const modules: Record<string, PluginModule> = {
    c: {
      init: () => {
        throw new Error('c 初始化失败');
      },
      dispose: () => void events.push('dispose:c'),
    },
    b: { init: () => void events.push('init:b'), dispose: () => void events.push('dispose:b') },
    a: { init: () => void events.push('init:a'), dispose: () => void events.push('dispose:a') },
    d: { init: () => void events.push('init:d') },
  };
  const loader = makeLoader(
    [
      m('a', '1.0.0', { requires: { b: '^1.0.0' } }),
      m('b', '1.0.0', { requires: { c: '^1.0.0' } }),
      m('c', '1.0.0'),
      m('d', '1.0.0'),
    ],
    [{ name: 'a' }, { name: 'd' }],
    modules,
  );
  const report = await loader.initAll();
  assert.deepEqual(report.initialized, ['d']);
  assert.deepEqual(
    report.failed.map((f) => f.name),
    ['c'],
  );
  assert.deepEqual(
    report.unloaded.map((u) => u.name).sort(),
    ['a', 'b'],
  );
  assert.equal(loader.getState('c'), 'failed');
  assert.equal(loader.getState('b'), 'unloaded');
  assert.equal(loader.getState('a'), 'unloaded');
  assert.equal(loader.getState('d'), 'initialized');
  assert.ok(loader.getPlugin('d') !== undefined);
  assert.equal(loader.getPlugin('a'), undefined);
  // b / a 从未初始化，不应触发 init；c 失败后被 dispose
  assert.deepEqual(events, ['dispose:c', 'init:d']);
});

test('动态请求未在计划中的插件被拒绝', async () => {
  const modules: Record<string, PluginModule> = {
    solo: {
      init: async (ctx: PluginContext) => {
        await ctx.require('ghost');
      },
    },
  };
  const loader = makeLoader([m('solo', '1.0.0')], [{ name: 'solo' }], modules);
  const report = await loader.initAll();
  assert.equal(report.failed.length, 1);
  assert.ok(String((report.failed[0].error as Error).message).includes('未在加载计划中'));
});

test('启动与停止按拓扑正序 / 逆序执行', async () => {
  const events: string[] = [];
  const mod = (name: string): PluginModule => ({
    init: () => void events.push(`init:${name}`),
    start: () => void events.push(`start:${name}`),
    stop: () => void events.push(`stop:${name}`),
  });
  const loader = makeLoader(
    [
      m('a', '1.0.0', { requires: { b: '^1.0.0' } }),
      m('b', '1.0.0', { requires: { c: '^1.0.0' } }),
      m('c', '1.0.0'),
    ],
    [{ name: 'a' }],
    { a: mod('a'), b: mod('b'), c: mod('c') },
  );
  await loader.initAll();
  await loader.startAll();
  await loader.stopAll();
  assert.deepEqual(events, [
    'init:c',
    'init:b',
    'init:a',
    'start:c',
    'start:b',
    'start:a',
    'stop:a',
    'stop:b',
    'stop:c',
  ]);
  assert.equal(loader.getState('a'), 'stopped');
});
