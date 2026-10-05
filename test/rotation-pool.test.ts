import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RotationPool } from '../src/rotation-pool.ts';

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

async function* arraySource<T>(values: T[], delayMs = 0): AsyncGenerator<T> {
  for (const value of values) {
    if (delayMs > 0) await sleep(delayMs);
    yield value;
  }
}

async function* throwingSource<T>(
  values: T[],
  failAt: number,
  error: unknown,
): AsyncGenerator<T> {
  for (let index = 0; index <= failAt; index++) {
    if (index === failAt) throw error;
    yield values[index];
  }
}

/** 完全由测试手动推进的生成器：每次 next() 挂起，等待 resolveNext/resolveDone。 */
function manualSource<T>() {
  const pending: Array<(result: IteratorResult<T>) => void> = [];
  let nextCalls = 0;
  let returnCalls = 0;
  const iterator: AsyncIterator<T> = {
    next() {
      nextCalls += 1;
      return new Promise<IteratorResult<T>>((resolve) => pending.push(resolve));
    },
    return() {
      returnCalls += 1;
      return Promise.resolve({ done: true, value: undefined });
    },
  };
  return {
    iterator,
    get nextCalls() {
      return nextCalls;
    },
    get returnCalls() {
      return returnCalls;
    },
    pendingCount: () => pending.length,
    resolveNext(value: T) {
      const resolve = pending.shift();
      if (!resolve) throw new Error('manualSource: no pending pull');
      resolve({ done: false, value });
    },
    resolveDone() {
      const resolve = pending.shift();
      if (!resolve) throw new Error('manualSource: no pending pull');
      resolve({ done: true, value: undefined });
    },
  };
}

/** 统计拉取并发度与调用次数的包装器。 */
function counted<T>(source: AsyncIterable<T>) {
  const stats = { calls: 0, inFlight: 0, maxInFlight: 0, returned: 0 };
  const inner = source[Symbol.asyncIterator]();
  const iterator: AsyncIterator<T> = {
    async next() {
      stats.calls += 1;
      stats.inFlight += 1;
      stats.maxInFlight = Math.max(stats.maxInFlight, stats.inFlight);
      try {
        return await inner.next();
      } finally {
        stats.inFlight -= 1;
      }
    },
    async return(value?: unknown) {
      stats.returned += 1;
      if (inner.return) await inner.return(value);
      return { done: true, value: undefined };
    },
  };
  return { iterator, stats };
}

async function collect<T>(pool: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = [];
  for await (const item of pool) out.push(item);
  return out;
}

test('按轮转顺序交错输出，长度不一致时耗尽的生成器被跳过', async () => {
  const pool = new RotationPool<string>();
  pool.add('A', arraySource(['a1', 'a2', 'a3']));
  pool.add('B', arraySource(['b1', 'b2']));
  pool.add('C', arraySource(['c1', 'c2', 'c3', 'c4']));

  assert.deepEqual(await collect(pool), [
    'a1', 'b1', 'c1',
    'a2', 'b2', 'c2',
    'a3', 'c3',
    'c4',
  ]);
});

test('消费者背压：无请求不拉取，每个生成器任意时刻最多一个未完成拉取', async () => {
  const sources = [
    counted(arraySource([1, 2, 3, 4, 5, 6], 3)),
    counted(arraySource([10, 20, 30, 40, 50, 60], 1)),
    counted(arraySource([100, 200, 300, 400, 500, 600], 2)),
  ];
  const pool = new RotationPool<number>();
  sources.forEach((source, index) => pool.add(`g${index}`, source.iterator));

  // 消费者尚未请求，不允许有任何拉取发生。
  await sleep(10);
  for (const source of sources) assert.equal(source.stats.calls, 0);

  const items = await collect(pool);
  assert.equal(items.length, 18);
  for (const source of sources) {
    assert.ok(
      source.stats.maxInFlight <= 1,
      `maxInFlight should be <= 1, got ${source.stats.maxInFlight}`,
    );
    // 6 项 + 1 次得到 done 的拉取，没有多余拉取。
    assert.equal(source.stats.calls, 7);
  }
});

test('暂停恰好发生在拉取完成之前：结果保留到恢复后交付', async () => {
  const a = manualSource<string>();
  const b = counted(arraySource(['b1', 'b2', 'b3']));
  const pool = new RotationPool<string>();
  pool.add('A', a.iterator);
  pool.add('B', b.iterator);

  // 第一轮：A 的第一次拉取在途，手动完成它。
  const first = pool.next();
  await tick(); // 等待池子发出第一次拉取
  assert.equal(a.pendingCount(), 1);
  a.resolveNext('a1');
  assert.deepEqual(await first, { done: false, value: 'a1' });

  // 交付后池子为 A 发起有界预取，第二次拉取在途。
  assert.equal(a.pendingCount(), 1);
  assert.equal(a.nextCalls, 2);

  // 暂停恰好落在这次拉取完成之前。
  pool.pause('A');
  a.resolveNext('a2'); // 拉取在暂停之后才完成
  await tick();
  assert.equal(a.pendingCount(), 0);

  // 已完成的结果必须被保留：轮转跳过 A，只交付 B 的项。
  assert.equal((await pool.next()).value, 'b1');
  assert.equal((await pool.next()).value, 'b2');
  // 暂停期间没有向 A 发起任何新拉取。
  assert.equal(a.nextCalls, 2);

  // 恢复后，暂停前完成的结果被交付，且交付前不需要新拉取。
  pool.resume('A');
  assert.equal((await pool.next()).value, 'a2');
  // 交付后恢复有界预取，仍然最多一个在途拉取。
  assert.equal(a.nextCalls, 3);
  assert.equal(a.pendingCount(), 1);

  const closing = pool.close();
  a.resolveDone();
  await closing;
});

test('暂停空闲生成器：暂停期间不发起拉取，恢复后正常拉取', async () => {
  const a = manualSource<string>();
  const pool = new RotationPool<string>();
  pool.add('A', a.iterator);
  pool.add('B', arraySource(['b1']));

  pool.pause('A'); // 空闲时暂停
  assert.equal((await pool.next()).value, 'b1');
  await tick();
  assert.equal(a.nextCalls, 0); // 暂停期间零拉取

  pool.resume('A');
  const pending = pool.next();
  await tick(); // 等待池子发出拉取
  assert.equal(a.nextCalls, 1);
  a.resolveNext('a1');
  assert.equal((await pending).value, 'a1');

  const closing = pool.close();
  a.resolveDone(); // 让 A 的在途预取落定，close 才能完成
  await closing;
});

test('全部暂停时消费者挂起，恢复后继续', async () => {
  const a = manualSource<string>();
  const b = manualSource<string>();
  const pool = new RotationPool<string>();
  pool.add('A', a.iterator);
  pool.add('B', b.iterator);
  pool.pause('A');
  pool.pause('B');

  const pending = pool.next();
  let settled = false;
  void pending.then(() => {
    settled = true;
  });
  await tick();
  await tick();
  assert.equal(settled, false); // 没有可用生成器，消费者等待

  pool.resume('B');
  await tick();
  assert.equal(b.pendingCount(), 1);
  b.resolveNext('b1');
  assert.equal((await pending).value, 'b1');

  const closing = pool.close();
  b.resolveDone(); // 让 B 的在途预取落定，close 才能完成
  await closing;
});

test('异常隔离：单个生成器抛错不影响其他生成器', async () => {
  const boom = new Error('boom');
  const errors: Array<{ id: string; error: unknown }> = [];
  const pool = new RotationPool<string>({
    onError: (id, error) => errors.push({ id, error }),
  });
  pool.add('A', arraySource(['a1', 'a2']));
  pool.add('B', throwingSource(['b1'], 1, boom));
  pool.add('C', arraySource(['c1', 'c2']));

  assert.deepEqual(await collect(pool), ['a1', 'b1', 'c1', 'a2', 'c2']);
  assert.deepEqual(errors, [{ id: 'B', error: boom }]);

  const states = new Map(pool.snapshot().map((entry) => [entry.id, entry]));
  assert.equal(states.get('A')?.state, 'done');
  assert.equal(states.get('B')?.state, 'failed');
  assert.equal(states.get('B')?.error, boom);
  assert.equal(states.get('C')?.state, 'done');
});

test('提前结束：close 等待在途拉取落定并调用所有 return', async () => {
  const a = manualSource<string>();
  const b = counted(arraySource(['b1', 'b2', 'b3']));
  const pool = new RotationPool<string>();
  pool.add('A', a.iterator);
  pool.add('B', b.iterator);

  const first = pool.next();
  await tick(); // 等待池子发出第一次拉取
  a.resolveNext('a1');
  assert.equal((await first).value, 'a1');
  assert.equal(a.pendingCount(), 1); // A 的预取在途
  assert.equal((await pool.next()).value, 'b1');

  const closing = pool.close();
  let closed = false;
  void closing.then(() => {
    closed = true;
  });
  await tick();
  assert.equal(closed, false); // close 等待 A 的在途拉取落定

  a.resolveNext('a2'); // 让在途拉取落定
  await closing;
  assert.equal(closed, true);
  assert.equal(a.returnCalls, 1);
  assert.equal(b.stats.returned, 1);

  // 结束后消费立即得到 done，且不再产生新拉取。
  assert.deepEqual(await pool.next(), { done: true, value: undefined });
  assert.equal(a.nextCalls, 2);
});

test('for-await 提前 break 触发提前结束', async () => {
  const a = counted(arraySource([1, 2, 3, 4, 5], 1));
  const pool = new RotationPool<number>();
  pool.add('A', a.iterator);

  for await (const item of pool) {
    assert.equal(item, 1);
    break;
  }
  assert.equal(a.stats.returned, 1);
  assert.equal(pool.closed, true);
  assert.deepEqual(await pool.next(), { done: true, value: undefined });
});

test('空池立即结束；非法操作抛错', async () => {
  const pool = new RotationPool<number>();
  assert.deepEqual(await pool.next(), { done: true, value: undefined });

  const pool2 = new RotationPool<number>();
  pool2.add('A', arraySource([1]));
  assert.throws(() => pool2.add('A', arraySource([2])), /duplicate id/);
  assert.throws(() => pool2.pause('nope'), /unknown generator/);
  await pool2.close();
  assert.throws(() => pool2.add('B', arraySource([3])), /after close/);
});
