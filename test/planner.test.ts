import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createPlan } from '../src/planner.ts';
import type { PluginManifest } from '../src/manifest.ts';

const m = (name: string, version: string, extra: Partial<PluginManifest> = {}): PluginManifest => ({
  name,
  version,
  ...extra,
});

test('硬依赖拓扑排序，选择最高兼容版本', () => {
  const plan = createPlan(
    [
      m('app', '1.0.0', { requires: { core: '^1.0.0' } }),
      m('core', '1.0.0'),
      m('core', '1.2.0'),
      m('core', '2.0.0'),
    ],
    [{ name: 'app' }],
  );
  assert.deepEqual(
    plan.order.map((p) => `${p.name}@${p.version}`),
    ['core@1.2.0', 'app@1.0.0'],
  );
  assert.deepEqual(plan.rejected, []);
});

test('冲突范围驱动版本选择：自动避开冲突版本', () => {
  const plan = createPlan(
    [m('a', '1.0.0', { conflicts: { b: '^1.0.0' } }), m('b', '1.5.0'), m('b', '2.0.0')],
    [{ name: 'a' }, { name: 'b' }],
  );
  const picked = Object.fromEntries(plan.order.map((p) => [p.name, p.version]));
  assert.deepEqual(picked, { a: '1.0.0', b: '2.0.0' });
  assert.deepEqual(plan.rejected, []);
});

test('不可调和的冲突：拒绝后请求的根并给出最小解释，独立插件保留', () => {
  const plan = createPlan(
    [m('x', '1.0.0', { conflicts: { y: '<2.0.0' } }), m('y', '1.0.0'), m('z', '1.0.0')],
    [{ name: 'x' }, { name: 'y' }, { name: 'z' }],
  );
  assert.deepEqual(
    plan.order.map((p) => p.name).sort(),
    ['x', 'z'],
  );
  assert.equal(plan.rejected.length, 1);
  const rejection = plan.rejected[0];
  assert.equal(rejection.name, 'y');
  // 最小解释：两条根请求 + 一条冲突声明，缺一不可
  assert.equal(rejection.explanation.constraints.length, 3);
  assert.ok(rejection.explanation.constraints.includes('请求 x@*'));
  assert.ok(rejection.explanation.constraints.includes('请求 y@*'));
  assert.ok(rejection.explanation.constraints.includes('x@1.0.0 与 y@<2.0.0 冲突'));
  assert.deepEqual(rejection.explanation.plugins, ['x', 'y']);
});

test('缺失硬依赖：根被拒绝，解释最小，其他根不受影响', () => {
  const plan = createPlan(
    [m('a', '1.0.0', { requires: { ghost: '^1.0.0' } }), m('ok', '1.0.0')],
    [{ name: 'a' }, { name: 'ok' }],
  );
  assert.deepEqual(
    plan.order.map((p) => p.name),
    ['ok'],
  );
  assert.equal(plan.rejected.length, 1);
  const explanation = plan.rejected[0].explanation;
  assert.equal(explanation.constraints.length, 2);
  assert.ok(explanation.constraints.includes('请求 a@*'));
  assert.ok(explanation.constraints.includes('a@1.0.0 依赖 ghost@^1.0.0'));
});

test('依赖版本不兼容：拒绝并给出最小解释', () => {
  const plan = createPlan(
    [m('a', '1.0.0', { requires: { b: '^2.0.0' } }), m('b', '1.0.0')],
    [{ name: 'a' }],
  );
  assert.equal(plan.order.length, 0);
  assert.equal(plan.rejected.length, 1);
  assert.deepEqual(plan.rejected[0].explanation.plugins, ['a', 'b']);
  assert.equal(plan.rejected[0].explanation.constraints.length, 2);
});

test('可选依赖：存在则启用并排在前面，缺失则跳过', () => {
  const withDep = createPlan(
    [m('o', '1.0.0', { optional: { dep: '^1.0.0' } }), m('dep', '1.1.0')],
    [{ name: 'o' }],
  );
  assert.deepEqual(
    withDep.order.map((p) => p.name),
    ['dep', 'o'],
  );
  assert.ok(withDep.notes.some((n) => n.includes('启用可选依赖 dep@^1.0.0')));

  const withoutDep = createPlan([m('o', '1.0.0', { optional: { ghost: '^1.0.0' } })], [{ name: 'o' }]);
  assert.deepEqual(
    withoutDep.order.map((p) => p.name),
    ['o'],
  );
  assert.ok(withoutDep.notes.some((n) => n.includes('已跳过')));
});

test('可选依赖引发冲突时跳过，宿主插件保留', () => {
  const plan = createPlan(
    [
      m('r', '1.0.0'),
      m('o', '1.0.0', { optional: { c: '^1.0.0' } }),
      m('c', '1.0.0', { conflicts: { r: '*' } }),
    ],
    [{ name: 'r' }, { name: 'o' }],
  );
  assert.deepEqual(
    plan.order.map((p) => p.name).sort(),
    ['o', 'r'],
  );
  assert.ok(plan.notes.some((n) => n.includes('已跳过 o@1.0.0 的可选依赖 c@^1.0.0')));
});

test('循环依赖：环中插件被排除并解释，独立插件保留', () => {
  const plan = createPlan(
    [
      m('p', '1.0.0', { requires: { q: '^1.0.0' } }),
      m('q', '1.0.0', { requires: { p: '^1.0.0' } }),
      m('free', '1.0.0'),
    ],
    [{ name: 'p' }, { name: 'free' }],
  );
  assert.deepEqual(
    plan.order.map((p2) => p2.name),
    ['free'],
  );
  const names = plan.rejected.map((r) => r.name).sort();
  assert.deepEqual(names, ['p', 'q']);
  assert.ok(plan.rejected.some((r) => r.explanation.summary.includes('循环依赖 p -> q -> p')));
});
