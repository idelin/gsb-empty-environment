import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FaultySink, MemorySink } from '../src/fault.ts';
import { Journal, assertIntact } from '../src/journal.ts';
import { TransactionalStore } from '../src/store.ts';
import { isProtoError } from '../src/errors.ts';

const enc = (s: string) => new TextEncoder().encode(s);

test('并发提交：串行化落盘，最终状态与日志一致且无泄漏', async () => {
  const sink = new MemorySink();
  const store = new TransactionalStore(new Journal(sink));

  const N = 64;
  await Promise.all(
    Array.from({ length: N }, (_, i) =>
      store.commit([{ kind: 'put', key: `key-${i}`, value: enc(`value-${i}`) }]),
    ),
  );

  assert.equal(store.size, N);
  for (let i = 0; i < N; i++) {
    assert.deepEqual(store.get(`key-${i}`), enc(`value-${i}`));
  }
  // 日志完好（无交错撕裂），重放后状态一致
  assertIntact(sink.read());
  const store2 = new TransactionalStore(new Journal(sink));
  assert.equal(store2.recover(), N);
  assert.equal(store2.size, N);
  assert.equal(store.stagedBufferCount, 0, '无 staged 缓冲泄漏');
});

test('并发下部分事务遇磁盘满：失败方回滚，成功方不受影响', async () => {
  const sink = new FaultySink(new MemorySink());
  const store = new TransactionalStore(new Journal(sink));

  // 确定性注入：第 2、4、6 次落盘触发磁盘满
  sink.plan.diskFullOnAppends = [2, 4, 6];
  const results = await Promise.allSettled(
    Array.from({ length: 20 }, (_, i) =>
      store.commit([{ kind: 'put', key: `k${i}`, value: enc(`${i}`) }]),
    ),
  );
  const rejected = results.filter((r) => r.status === 'rejected');
  assert.equal(rejected.length, 3, '应有 3 个事务被磁盘满拒绝');
  for (const r of rejected) {
    assert.ok(isProtoError((r as PromiseRejectedResult).reason, 'DISK_FULL'));
  }
  // 日志必须保持完好：被拒绝的事务没有留下半截记录
  assertIntact(sink.read());
  // 恢复后状态与当前内存状态一致
  const store2 = new TransactionalStore(new Journal(sink));
  assert.equal(store2.recover(), 17);
  assert.equal(store2.size, 17);
  assert.equal(store.size, 17);
  assert.equal(store.stagedBufferCount, 0);
});

test('同一 key 并发写：最后一次提交生效，日志可完整重放', async () => {
  const sink = new MemorySink();
  const store = new TransactionalStore(new Journal(sink));
  await Promise.all(
    Array.from({ length: 32 }, (_, i) =>
      store.commit([{ kind: 'put', key: 'hot', value: enc(`v${i}`) }]),
    ),
  );
  const finalValue = store.get('hot');
  const store2 = new TransactionalStore(new Journal(sink));
  assert.equal(store2.recover(), 32);
  assert.deepEqual(store2.get('hot'), finalValue, '重放结果应与内存终态一致');
});
