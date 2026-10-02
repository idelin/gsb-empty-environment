import test from 'node:test';
import assert from 'node:assert/strict';
import { AWLWWMap } from '../src/awlwwmap.ts';

/** 构造两个已同步的副本（同一初始状态）。 */
function syncedPair(): { a: AWLWWMap; b: AWLWWMap } {
  const seed = new AWLWWMap('seed');
  seed.setField('user_profile', 'name', 'Ada', 1000);
  seed.setField('user_profile', 'age', 30, 1001);
  const a = new AWLWWMap('nodeA');
  const b = new AWLWWMap('nodeB');
  a.merge(seed);
  b.merge(seed);
  return { a, b };
}

test('基本增删改查', () => {
  const m = new AWLWWMap('n1');
  m.set('k1', 'v1', 1);
  assert.equal(m.get('k1'), 'v1');
  assert.ok(m.has('k1'));
  assert.deepEqual(m.keys(), ['k1']);

  m.set('k1', 'v2', 2);
  assert.equal(m.get('k1'), 'v2');

  m.remove('k1');
  assert.equal(m.get('k1'), undefined);
  assert.ok(!m.has('k1'));
  assert.deepEqual(m.keys(), []);
});

test('LWW：时间戳大的写入获胜，时间戳相同按节点 ID 确定性仲裁', () => {
  const a = new AWLWWMap('nodeA');
  const b = new AWLWWMap('nodeB');
  a.set('k', 'fromA', 10);
  b.set('k', 'fromB', 20);
  a.merge(b);
  assert.equal(a.get('k'), 'fromB');

  // 同时戳并发：双向合并结果一致（nodeB > nodeA，B 胜）
  const c = new AWLWWMap('nodeA');
  const d = new AWLWWMap('nodeB');
  c.set('k', 'fromA', 10);
  d.set('k', 'fromB', 10);
  const cd = c.clone().merge(d);
  const dc = d.clone().merge(c);
  assert.equal(cd.get('k'), 'fromB');
  assert.equal(dc.get('k'), 'fromB');
});

test('验收边界：并发 删除根键 vs 修改子字段 → 更新优先，键与字段保留', () => {
  const { a, b } = syncedPair();

  // 节点 A：删除根键 user_profile
  a.remove('user_profile');
  assert.ok(!a.has('user_profile'));

  // 节点 B：并发修改 user_profile.age = 25（未观察到 A 的删除）
  b.setField('user_profile', 'age', 25, 2000);

  // 因果合并（双向都验证）
  const ab = a.clone().merge(b);
  const ba = b.clone().merge(a);

  for (const merged of [ab, ba]) {
    assert.ok(merged.has('user_profile'), 'user_profile 应被保留（Add-Wins）');
    assert.equal(merged.getField('user_profile', 'age'), 25, 'age 应为并发更新值 25');
    assert.equal(merged.getField('user_profile', 'name'), 'Ada', '既有子字段 name 不丢失');
    assert.deepEqual(merged.toObject(), { user_profile: { age: 25, name: 'Ada' } });
  }
});

test('Add-Wins：并发 删除键 vs 重写键 → 写入优先', () => {
  const a = new AWLWWMap('nodeA');
  const b = new AWLWWMap('nodeB');
  a.set('k', 'old', 1);
  b.merge(a);

  a.remove('k'); // A 删除
  b.set('k', 'new', 2); // B 并发重写

  a.merge(b);
  assert.equal(a.get('k'), 'new');
});

test('删除在因果上生效：先观察后删除，合并后键确实消失', () => {
  const a = new AWLWWMap('nodeA');
  const b = new AWLWWMap('nodeB');
  a.set('k', 'v', 1);
  b.merge(a); // B 先观察到写入
  b.remove('k'); // 再删除
  a.merge(b);
  assert.ok(!a.has('k'));
});

test('子字段动态增删改', () => {
  const m = new AWLWWMap('n1');
  m.setField('user_profile', 'age', 25, 1); // 键不存在时自动创建
  assert.equal(m.getField('user_profile', 'age'), 25);

  m.setField('user_profile', 'city', 'Shanghai', 2);
  m.setField('user_profile', 'age', 26, 3); // 改
  assert.deepEqual(m.toObject(), { user_profile: { age: 26, city: 'Shanghai' } });

  m.removeField('user_profile', 'city', 4); // 删
  assert.deepEqual(m.toObject(), { user_profile: { age: 26 } });
});

test('并发 删除根键 vs 删除子字段 → 键保留且子字段已删', () => {
  const { a, b } = syncedPair();
  a.remove('user_profile');
  b.removeField('user_profile', 'age', 2000);

  a.merge(b);
  assert.ok(a.has('user_profile'));
  assert.deepEqual(a.toObject(), { user_profile: { name: 'Ada' } });
});

test('并发子字段更新按 LWW 合并，双方字段都保留', () => {
  const { a, b } = syncedPair();
  a.setField('user_profile', 'city', 'Beijing', 2000);
  b.setField('user_profile', 'age', 25, 2001);

  a.merge(b);
  assert.deepEqual(a.toObject(), {
    user_profile: { age: 25, city: 'Beijing', name: 'Ada' },
  });
});

test('CRDT 性质：幂等、交换、结合', () => {
  const { a, b } = syncedPair();
  a.remove('user_profile');
  b.setField('user_profile', 'age', 25, 2000);

  // 幂等
  const once = a.clone().merge(b);
  const twice = once.clone().merge(b);
  assert.deepEqual(twice.toObject(), once.toObject());

  // 交换
  const ab = a.clone().merge(b);
  const ba = b.clone().merge(a);
  assert.deepEqual(ab.toObject(), ba.toObject());

  // 结合
  const c = new AWLWWMap('nodeC');
  c.setField('user_profile', 'city', 'Shanghai', 2002);
  const left = a.clone().merge(b).merge(c);
  const right = a.clone().merge(b.clone().merge(c));
  assert.deepEqual(left.toObject(), right.toObject());
});

test('合并不共享引用：合并后修改源副本不影响目标', () => {
  const a = new AWLWWMap('nodeA');
  const b = new AWLWWMap('nodeB');
  b.setField('user_profile', 'age', 25, 1);
  a.merge(b);

  b.setField('user_profile', 'age', 99, 2);
  b.remove('user_profile');
  assert.equal(a.getField('user_profile', 'age'), 25);
});
