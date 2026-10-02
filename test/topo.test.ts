import { test } from 'node:test';
import assert from 'node:assert/strict';
import { IncrementalTopo, CycleError } from '../src/incremental-topo.ts';

test('初始链 1->2->3->4 建立拓扑序', async () => {
  const g = new IncrementalTopo();
  await g.addEdge(1, 2);
  await g.addEdge(2, 3);
  await g.addEdge(3, 4);
  assert.deepEqual(g.getOrder(), [1, 2, 3, 4]);
  assert.ok(g.isValid());
});

test('环检测：4->2 被拒绝，返回 [2, 3, 4, 2]，拓扑序不变', async () => {
  const g = new IncrementalTopo();
  await g.addEdge(1, 2);
  await g.addEdge(2, 3);
  await g.addEdge(3, 4);
  const before = g.getOrder();

  const err = await g.addEdge(4, 2).then(
    () => assert.fail('should have thrown'),
    (e) => e,
  );
  assert.ok(err instanceof CycleError);
  assert.deepEqual(err.cycle, [2, 3, 4, 2]);

  // 原拓扑序未被修改
  assert.deepEqual(g.getOrder(), before);
  assert.ok(g.isValid());
});

test('局部重排：1->4 保持拓扑单调性', async () => {
  const g = new IncrementalTopo();
  await g.addEdge(1, 2);
  await g.addEdge(2, 3);
  await g.addEdge(3, 4);
  await g.addEdge(1, 4); // 快速路径：序号已满足，无需重排
  assert.deepEqual(g.getOrder(), [1, 2, 3, 4]);
  assert.ok(g.isValid());
});

test('双向局部重排：逆序边只影响局部区间', async () => {
  const g = new IncrementalTopo();
  // 两条独立链：a->b（序号 0,1），c->d（序号 2,3）
  await g.addEdge('a', 'b');
  await g.addEdge('c', 'd');
  const idxD = g.indexOf('d');
  // c->a 无环但序号违逆（label[c]=2 > label[a]=0），触发双向局部重排
  await g.addEdge('c', 'a');
  assert.ok(g.isValid());
  assert.ok(g.indexOf('c') < g.indexOf('a'));
  assert.ok(g.indexOf('a') < g.indexOf('b'));
  assert.ok(g.indexOf('b') < g.indexOf('d'));
  // 区间外节点 d 的序号不受影响
  assert.equal(g.indexOf('d'), idxD);
  assert.deepEqual(g.getOrder(), ['c', 'a', 'b', 'd']);
});

test('自环立即拒绝', async () => {
  const g = new IncrementalTopo();
  await g.addEdge(1, 2);
  const err = await g.addEdge(1, 1).catch((e) => e);
  assert.ok(err instanceof CycleError);
  assert.deepEqual(err.cycle, [1, 1]);
});

test('并发加边：串行化执行，结果一致且无环边被接受', async () => {
  const g = new IncrementalTopo();
  await g.addEdge(1, 2);
  await g.addEdge(2, 3);
  await g.addEdge(3, 4);

  // 并发提交：合法边 + 成环边混合
  const results = await Promise.allSettled([
    g.addEdge(1, 4),
    g.addEdge(4, 2), // 成环，应被拒绝
    g.addEdge(1, 3),
    g.addEdge(2, 4),
  ]);
  const rejected = results.filter((r) => r.status === 'rejected');
  assert.equal(rejected.length, 1);
  assert.ok((rejected[0] as PromiseRejectedResult).reason instanceof CycleError);
  assert.deepEqual(
    (rejected[0] as PromiseRejectedResult).reason.cycle,
    [2, 3, 4, 2],
  );
  assert.ok(g.isValid());
  assert.deepEqual(g.getOrder(), [1, 2, 3, 4]);
});

test('重复边幂等', async () => {
  const g = new IncrementalTopo();
  await g.addEdge(1, 2);
  await g.addEdge(1, 2);
  assert.deepEqual(g.getOrder(), [1, 2]);
});
