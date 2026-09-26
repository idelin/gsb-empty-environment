import assert from 'node:assert/strict';
import test from 'node:test';
import { Compositor } from '../src/compositor.ts';
import { compilePlan } from '../src/graph.ts';
import { solidLayer } from './helpers.ts';

test('循环依赖图被拒绝', () => {
  const c = new Compositor({ width: 2, height: 2, tileSize: 2 });
  c.upsertLayer(solidLayer('a', 2, 2, [1, 0, 0, 1], { inputs: ['b'] }));
  c.upsertLayer(solidLayer('b', 2, 2, [0, 1, 0, 1], { inputs: ['a'] }));
  assert.throws(() => c.commit(), /cycle/);
  assert.equal(c.currentVersion, 0, '拒绝后不得发布任何版本');
});

test('依赖边决定合成顺序，与插入顺序无关', () => {
  const c = new Compositor({ width: 2, height: 2, tileSize: 2 });
  // 先插入上层，但用 inputs 声明它必须排在 bg 之后
  c.upsertLayer(solidLayer('top', 2, 2, [1, 0, 0, 1], { inputs: ['bg'] }));
  c.upsertLayer(solidLayer('bg', 2, 2, [0, 0, 1, 1]));
  const v = c.commit().version;
  const [r, , b] = c.readPixel(v, 0, 0);
  assert.ok(Math.abs(r - 1) < 1e-6 && Math.abs(b) < 1e-6, 'top 必须最后合成');
});

test('未知依赖与未知混合算子被拒绝', () => {
  assert.throws(
    () => compilePlan([solidLayer('a', 1, 1, [1, 1, 1, 1], { inputs: ['ghost'] })], 1, 1),
    /unknown layer/,
  );
  const bad = solidLayer('a', 1, 1, [1, 1, 1, 1]);
  (bad as { blend: string }).blend = 'overlay-ish';
  assert.throws(() => compilePlan([bad], 1, 1), /unknown blend/);
});

test('已预乘图层绝不重复预乘，颜色转换按需插入', () => {
  const pre = solidLayer('pre', 1, 1, [0.5, 0, 0, 0.5], { premultiplied: true });
  const straight = solidLayer('straight', 1, 1, [1, 0, 0, 0.5]);
  let plan = compilePlan([pre, straight], 1, 1);
  assert.equal(plan.workingSpace, 'srgb');
  assert.equal(plan.steps.find((s) => s.layer.id === 'pre')!.premultiply, false);
  assert.equal(plan.steps.find((s) => s.layer.id === 'straight')!.premultiply, true);
  assert.ok(plan.steps.every((s) => !s.convert), '同空间不应插入转换');

  const linearLayer = solidLayer('lin', 1, 1, [1, 1, 1, 1], { colorSpace: 'linear' });
  plan = compilePlan([pre, linearLayer], 1, 1);
  assert.equal(plan.workingSpace, 'linear');
  assert.equal(plan.steps.find((s) => s.layer.id === 'pre')!.convert, true, '异空间须插入转换');
  assert.equal(plan.steps.find((s) => s.layer.id === 'lin')!.convert, false);
});
