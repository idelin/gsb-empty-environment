import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RefreshCoordinator, type SourcePayload } from '../src/store.ts';

const SOURCES = ['A', 'B', 'C'];

function ok(version: number, tag = ''): SourcePayload {
  return { version, data: { tag: `${tag}v${version}` } };
}

test('全部源刷新到同一逻辑版本后原子发布', async () => {
  const c = new RefreshCoordinator(SOURCES);
  const summary = await c.refreshAll(2, { A: async () => ok(2), B: async () => ok(2), C: async () => ok(2) });
  assert.equal(summary.committed, true);
  assert.equal(c.getView().version, 2);
  assert.deepEqual(Object.keys(c.getView().data).sort(), SOURCES);
});

test('部分失败：保持旧一致视图并报告失败源', async () => {
  const c = new RefreshCoordinator(SOURCES);
  await c.refreshAll(1, { A: async () => ok(1), B: async () => ok(1), C: async () => ok(1) });
  const before = c.getView();

  const summary = await c.refreshAll(2, {
    A: async () => ok(2),
    B: async () => { throw new Error('network timeout'); },
    C: async () => ok(2),
  });

  assert.equal(summary.committed, false);
  assert.deepEqual(summary.failedSources, ['B']);
  assert.equal(c.getView(), before, '视图对象未被替换，仍是旧一致视图');
  assert.equal(c.getView().version, 1);
  assert.equal(c.getFailedSources().get('B'), 'network timeout');
});

test('A新B旧不能冒充整组已刷新（版本不一致记为该源失败）', async () => {
  const c = new RefreshCoordinator(SOURCES);
  const summary = await c.refreshAll(2, {
    A: async () => ok(2),
    B: async () => ok(1), // B 返回旧版本数据
    C: async () => ok(2),
  });
  assert.equal(summary.committed, false);
  assert.equal(c.getView().version, 0, '整组未发布');
  assert.deepEqual(summary.failedSources, ['B']);
  assert.match(c.getFailedSources().get('B')!, /version-mismatch/);
});

test('重试单源成功后恰好发布一次', async () => {
  const c = new RefreshCoordinator(SOURCES);
  await c.refreshAll(2, {
    A: async () => ok(2),
    B: async () => { throw new Error('boom'); },
    C: async () => ok(2),
  });

  const summary = await c.retrySource('B', async () => ok(2, 'retried-'));
  assert.equal(summary.committed, true);
  assert.equal(c.getView().version, 2);
  assert.deepEqual((c.getView().data as any).B, { tag: 'retried-v2' });

  const commits = c.getLog().filter((e) => e.type === 'Committed' && e.round === summary.round);
  assert.equal(commits.length, 1, 'Committed 事件恰好一次');

  // 提交后再上报/重试均被拒绝，不会二次发布
  const late = c.reportSource(summary.round, 'A', ok(2));
  assert.equal(late.accepted, false);
  await assert.rejects(() => c.retrySource('A', async () => ok(2)), /already committed/);
  assert.equal(c.getLog().filter((e) => e.type === 'Committed').length, 1);
});

test('过期上一轮结果不能混入下一轮（轮次围栏）', async () => {
  const c = new RefreshCoordinator(SOURCES);
  // 第一轮：B 失败，抓取承诺挂起
  let resolveStaleB!: (p: SourcePayload) => void;
  const staleFetch = new Promise<SourcePayload>((res) => { resolveStaleB = res; });
  const round1 = c.startRefresh(1);
  c.reportSource(round1, 'A', ok(1));
  c.reportSource(round1, 'C', ok(1));
  const pending = c.retrySource('B', () => staleFetch);

  // 第二轮开启，旧轮作废
  const round2 = c.startRefresh(2);
  c.reportSource(round2, 'A', ok(2));
  c.reportSource(round2, 'C', ok(2));

  // 旧轮的 B 迟到：被拒绝，且不会满足新一轮
  resolveStaleB(ok(1, 'stale-'));
  await pending;
  assert.equal(c.getView().version, 0, '旧轮结果未混入，新一轮尚未齐');
  assert.equal(c.getLog().filter((e) => e.type === 'SourceLoaded' && e.source === 'B').length, 0);

  // 新一轮用正确版本补齐 B 才提交
  const r = c.reportSource(round2, 'B', ok(2));
  assert.equal(r.accepted, true);
  assert.equal(c.getView().version, 2);
});

test('读者看不到事务中间态（快照隔离）', async () => {
  const c = new RefreshCoordinator(SOURCES);
  await c.refreshAll(1, { A: async () => ok(1), B: async () => ok(1), C: async () => ok(1) });
  const stable = c.getView();

  const round = c.startRefresh(2);
  c.reportSource(round, 'A', ok(2)); // 事务进行中：1/3 已到位
  assert.equal(c.getView(), stable, '中间态对读者不可见');
  c.reportSource(round, 'B', ok(2));
  assert.equal(c.getView(), stable);
  c.reportSource(round, 'C', ok(2)); // 齐活 → 原子切换
  assert.notEqual(c.getView(), stable);
  assert.equal(c.getView().version, 2);
  assert.equal(stable.version, 1, '旧快照本身不被修改');
});

test('已发布视图深冻结，读者无法篡改', async () => {
  const c = new RefreshCoordinator(SOURCES);
  await c.refreshAll(1, { A: async () => ok(1), B: async () => ok(1), C: async () => ok(1) });
  const view = c.getView();
  assert.throws(() => { (view as any).version = 99; }, TypeError);
  assert.throws(() => { (view.data as any).A = 'hacked'; }, TypeError);
});

test('状态操作可回放：从事件日志重建出相同状态', async () => {
  const c = new RefreshCoordinator(SOURCES);
  await c.refreshAll(1, { A: async () => ok(1), B: async () => ok(1), C: async () => ok(1) });
  await c.refreshAll(2, {
    A: async () => ok(2),
    B: async () => { throw new Error('boom'); },
    C: async () => ok(2),
  });
  await c.retrySource('B', async () => ok(2, 'retried-'));

  const replayed = RefreshCoordinator.replay(SOURCES, c.getLog());
  assert.deepEqual(replayed.getView(), c.getView());
  assert.deepEqual([...replayed.getFailedSources()], [...c.getFailedSources()]);
  assert.equal(replayed.currentRound, c.currentRound);
  assert.deepEqual(replayed.getLog(), c.getLog());
});
