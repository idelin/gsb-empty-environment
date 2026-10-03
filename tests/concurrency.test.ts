import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FaultInjector } from '../src/faults.ts';
import { FaultyBlockDevice } from '../src/device.ts';
import { DurableStore } from '../src/store.ts';
import { ResourceTracker } from '../src/tracker.ts';
import { InjectedFaultError } from '../src/errors.ts';

const CAPACITY = 4 * 1024 * 1024;

test('并发安全: 100 个并发事务全部串行提交, 无丢失无交错', async () => {
  const tracker = new ResourceTracker();
  const injector = new FaultInjector(42);
  const device = new FaultyBlockDevice(CAPACITY, injector);
  const store = DurableStore.create(device, tracker);

  const N = 100;
  await Promise.all(
    Array.from({ length: N }, (_, i) =>
      store.transact([{ key: `key-${i}`, value: Uint8Array.from([i & 0xff]) }]),
    ),
  );

  assert.equal(store.size, N, '全部事务生效');
  for (let i = 0; i < N; i++) {
    assert.deepEqual([...store.get(`key-${i}`)!], [i & 0xff]);
  }

  // 崩溃恢复后状态完整
  const { store: recovered, report } = DurableStore.recover(device.reboot(), tracker);
  assert.equal(recovered.size, N);
  assert.equal(report.committed.length, N, '日志中恰好 N 个已提交事务');
  assert.equal(report.rolledBack, 0);
  tracker.assertNoLeaks();
});

test('并发安全: 随机故障注入下, 成功事务集合与恢复结果严格一致', async () => {
  const tracker = new ResourceTracker();
  const injector = new FaultInjector(0xc0ffee);
  // 10% 断电 + 5% 磁盘满
  injector.arm('power-loss', { probability: 0.1 });
  injector.arm('disk-full', { probability: 0.05 });
  const device = new FaultyBlockDevice(CAPACITY, injector, 0.4);
  const store = DurableStore.create(device, tracker);

  const N = 60;
  const succeeded = new Set<number>();
  const results = await Promise.allSettled(
    Array.from({ length: N }, (_, i) =>
      store.transact([{ key: `tx-${i}`, value: Uint8Array.from([i]) }]),
    ),
  );
  results.forEach((r, i) => {
    if (r.status === 'fulfilled') succeeded.add(i);
    else {
      assert.ok(r.reason instanceof InjectedFaultError, '失败只能来自注入故障');
    }
  });
  injector.disarmAll();

  assert.ok(succeeded.size > 0, '至少部分事务成功');
  assert.ok(succeeded.size < N, '确实有事务被故障打断');

  // 内存态: 恰好是成功事务的键
  assert.equal(store.size, succeeded.size);
  for (const i of succeeded) {
    assert.deepEqual([...store.get(`tx-${i}`)!], [i]);
  }

  // 恢复后: 与内存态严格一致 (原子性保证)
  const { store: recovered } = DurableStore.recover(device.reboot(), tracker);
  assert.equal(recovered.size, succeeded.size);
  assert.deepEqual(recovered.keys(), store.keys());
  for (const i of succeeded) {
    assert.deepEqual([...recovered.get(`tx-${i}`)!], [i]);
  }
  tracker.assertNoLeaks();
});

test('无泄漏: 事务资源在成功/失败路径上都被释放', async () => {
  const tracker = new ResourceTracker();
  const injector = new FaultInjector(11);
  const device = new FaultyBlockDevice(CAPACITY, injector);
  const store = DurableStore.create(device, tracker);

  await store.transact([{ key: 'ok', value: Uint8Array.from([1]) }]);

  injector.arm('power-loss', { probability: 1.0 }); // 必失败
  for (let i = 0; i < 10; i++) {
    await assert.rejects(
      store.transact([{ key: `fail-${i}`, value: Uint8Array.from([2]) }]),
      InjectedFaultError,
    );
  }
  injector.disarmAll();

  assert.equal(tracker.liveCount, 0, '所有事务句柄已释放');
  tracker.assertNoLeaks();
  assert.equal(store.size, 1, '失败事务不留痕迹');
});

test('无泄漏: 重复释放被检测', () => {
  const tracker = new ResourceTracker();
  tracker.acquire('x');
  tracker.release('x');
  assert.throws(() => tracker.release('x'), /重复释放/);
  tracker.assertNoLeaks();
});

test('并发压力: 故障注入 + 检查点交错, 最终状态可恢复且一致', async () => {
  const tracker = new ResourceTracker();
  const injector = new FaultInjector(777);
  injector.arm('power-loss', { probability: 0.05 });
  const device = new FaultyBlockDevice(CAPACITY, injector, 0.5);
  const store = DurableStore.create(device, tracker);

  const committed = new Set<number>();
  for (let round = 0; round < 20; round++) {
    const batch = Array.from({ length: 5 }, (_, j) => round * 5 + j);
    const results = await Promise.allSettled(
      batch.map((i) =>
        store.transact([{ key: `k-${i}`, value: Uint8Array.from([i & 0xff]) }]),
      ),
    );
    results.forEach((r, i) => {
      if (r.status === 'fulfilled') committed.add(batch[i]);
    });
    if (round % 5 === 4) store.checkpoint();
  }
  injector.disarmAll();

  const { store: recovered, report } = DurableStore.recover(device.reboot(), tracker);
  assert.equal(recovered.size, committed.size);
  assert.deepEqual(recovered.keys(), store.keys());
  assert.ok(report.checkpointOffset >= 0, '检查点已固化');
  tracker.assertNoLeaks();
});
