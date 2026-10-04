import { test } from "node:test";
import assert from "node:assert/strict";
import { RoundRobinChannel, roundRobin } from "../src/round-robin.ts";

const flush = () => new Promise<void>((resolve) => setImmediate(resolve));
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (error: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function* from<T>(items: T[]): AsyncGenerator<T> {
  for (const item of items) yield item;
}

/** 完全手动控制的源：每次 next() 记录一个 deferred，由测试自行落定。 */
function manualSource<T>() {
  const pulls: Array<Deferred<IteratorResult<T>>> = [];
  const state = {
    pulls,
    returned: false,
    source: null as unknown as AsyncIterableIterator<T>,
  };
  state.source = {
    [Symbol.asyncIterator]() {
      return state.source;
    },
    next(): Promise<IteratorResult<T>> {
      const d = deferred<IteratorResult<T>>();
      pulls.push(d);
      return d.promise;
    },
    return(): Promise<IteratorResult<T>> {
      state.returned = true;
      return Promise.resolve({ value: undefined, done: true });
    },
  };
  return state;
}

/** 带统计的源：记录拉取并发数，用于验证“最多一个未完成拉取”。 */
function instrumentedSource<T>(
  itemCount: number,
  delayMs: number,
  makeValue: (index: number) => T,
) {
  const stats = { started: 0, settled: 0, maxOutstanding: 0, returned: false };
  let remaining = itemCount;
  let valueIndex = 0;
  const source: AsyncIterableIterator<T> = {
    [Symbol.asyncIterator]() {
      return source;
    },
    next(): Promise<IteratorResult<T>> {
      stats.started++;
      stats.maxOutstanding = Math.max(
        stats.maxOutstanding,
        stats.started - stats.settled,
      );
      return new Promise((resolve) => {
        setTimeout(() => {
          stats.settled++;
          resolve(
            remaining-- > 0
              ? { value: makeValue(valueIndex++), done: false }
              : { value: undefined, done: true },
          );
        }, delayMs);
      });
    },
    return(): Promise<IteratorResult<T>> {
      stats.returned = true;
      return Promise.resolve({ value: undefined, done: true });
    },
  };
  return { source, stats };
}

test("每轮各取一项，按加入顺序交错，耗尽的源自动退出", async () => {
  const channel = roundRobin([
    from(["a1", "a2", "a3"]),
    from(["b1", "b2"]),
    from(["c1"]),
  ]);
  const out: string[] = [];
  for await (const value of channel) out.push(value);
  assert.deepEqual(out, ["a1", "b1", "c1", "a2", "b2", "a3"]);
  assert.equal(channel.activeCount, 0);
});

test("背压：消费者不拉就不取，且每个源最多一个未完成拉取", async () => {
  const channel = new RoundRobinChannel<number>();
  const instruments = [
    instrumentedSource(5, 2, (i) => i),
    instrumentedSource(5, 4, (i) => i),
    instrumentedSource(5, 6, (i) => i),
  ];
  for (const { source } of instruments) channel.add(source);

  const iterator = channel.stream();
  // 消费者尚未调用 next()：不允许有任何拉取发生。
  await flush();
  for (const { stats } of instruments) assert.equal(stats.started, 0);

  // 慢消费者：逐项拉取并故意拖延。
  let consumed = 0;
  for (;;) {
    const { done } = await iterator.next();
    if (done) break;
    consumed++;
    await sleep(3);
  }
  assert.equal(consumed, 15);
  for (const { stats } of instruments) {
    assert.ok(
      stats.maxOutstanding <= 1,
      `maxOutstanding should be <= 1, got ${stats.maxOutstanding}`,
    );
  }
});

test("异常隔离：一个源抛错被上报并移除，其余源继续", async () => {
  const errors: Array<{ error: unknown; id: number }> = [];
  const channel = new RoundRobinChannel<string>({
    onSourceError: (error, id) => errors.push({ error, id }),
  });
  async function* failing(): AsyncGenerator<string> {
    yield "b1";
    throw new Error("boom");
  }
  channel.add(from(["a1", "a2"]));
  const failingId = channel.add(failing());
  channel.add(from(["c1", "c2"]));

  const out: string[] = [];
  for await (const value of channel) out.push(value);

  assert.deepEqual(out, ["a1", "b1", "c1", "a2", "c2"]);
  assert.equal(errors.length, 1);
  assert.equal(errors[0].id, failingId);
  assert.equal((errors[0].error as Error).message, "boom");
});

test("提前结束：消费者 break 后所有源都被 return 清理", async () => {
  const cleaned = { a: false, b: false };
  async function* infinite(which: "a" | "b"): AsyncGenerator<string> {
    try {
      let i = 0;
      for (;;) yield `${which}${i++}`;
    } finally {
      cleaned[which] = true;
    }
  }
  const channel = roundRobin([infinite("a"), infinite("b")]);

  let count = 0;
  for await (const _ of channel) {
    if (++count === 3) break;
  }
  assert.equal(count, 3);
  assert.deepEqual(cleaned, { a: true, b: true });
});

test("暂停空闲的源：暂停期间不发起任何拉取，恢复后继续轮转", async () => {
  const channel = new RoundRobinChannel<string>();
  channel.add(from(["a0", "a1", "a2", "a3"]));
  const b = instrumentedSource(2, 0, (i) => `b${i}`);
  const bId = channel.add(b.source);

  // 源尚空闲（没有任何拉取）时暂停。
  assert.equal(channel.pause(bId), true);

  const out: string[] = [];
  for await (const value of channel) {
    out.push(value);
    if (out.length === 2) {
      // 暂停期间：B 没有收到任何拉取。
      assert.equal(b.stats.started, 0);
      assert.equal(channel.resume(bId), true);
    }
  }

  assert.deepEqual(out, ["a0", "a1", "a2", "b0", "a3", "b1"]);
  // 2 次取项 + 1 次发现耗尽（迭代器协议必须多拉一次才知道 done）。
  assert.equal(b.stats.started, 3);
  assert.ok(b.stats.maxOutstanding <= 1);
});

test("暂停恰好发生在拉取完成之前：结果缓冲到恢复，且不发起新拉取", async () => {
  const channel = new RoundRobinChannel<string>();
  const a = manualSource<string>();
  const b = manualSource<string>();
  channel.add(a.source);
  const bId = channel.add(b.source);

  const iterator = channel.stream();

  // 第一轮：两个源各收到恰好一个拉取。
  const firstP = iterator.next();
  assert.equal(a.pulls.length, 1);
  assert.equal(b.pulls.length, 1);
  a.pulls[0].resolve({ value: "a1", done: false });
  assert.deepEqual(await firstP, { value: "a1", done: false });

  // 关键时序：B 的拉取仍在进行中，先暂停，再让拉取完成。
  assert.equal(channel.pause(bId), true);
  b.pulls[0].resolve({ value: "b1", done: false });
  await flush();

  // b1 必须被缓冲而不是发出：没有新拉取，下一个值仍来自 A。
  assert.equal(b.pulls.length, 1);
  const secondP = iterator.next();
  await flush();
  assert.equal(a.pulls.length, 2);
  a.pulls[1].resolve({ value: "a2", done: false });
  assert.deepEqual(await secondP, { value: "a2", done: false });
  assert.equal(b.pulls.length, 1);

  // 恢复：缓冲的 b1 在下一轮发出，且恢复本身不触发新拉取。
  assert.equal(channel.resume(bId), true);
  const thirdP = iterator.next();
  await flush();
  assert.equal(a.pulls.length, 3);
  a.pulls[2].resolve({ value: "a3", done: false });
  assert.deepEqual(await thirdP, { value: "a3", done: false });
  assert.equal(b.pulls.length, 1);
  assert.deepEqual(await iterator.next(), { value: "b1", done: false });
  assert.equal(b.pulls.length, 1);

  // 通道关闭后，两个源都被清理。
  await iterator.return(undefined);
  assert.equal(a.returned, true);
  assert.equal(b.returned, true);
});

test("全部源暂停时通道挂起等待，恢复后继续", async () => {
  const channel = new RoundRobinChannel<string>();
  const a = manualSource<string>();
  const b = manualSource<string>();
  const aId = channel.add(a.source);
  const bId = channel.add(b.source);
  channel.pause(aId);
  channel.pause(bId);

  const iterator = channel.stream();
  const nextP = iterator.next();
  await flush();

  // 所有源都暂停：没有任何拉取，next() 保持挂起。
  assert.equal(a.pulls.length, 0);
  assert.equal(b.pulls.length, 0);
  const raced = await Promise.race([
    nextP.then(() => "resolved" as const),
    flush().then(() => "pending" as const),
  ]);
  assert.equal(raced, "pending");

  // 恢复其中一个源后，通道被唤醒并继续。
  channel.resume(aId);
  await flush();
  assert.equal(a.pulls.length, 1);
  assert.equal(b.pulls.length, 0);
  a.pulls[0].resolve({ value: "a1", done: false });
  assert.deepEqual(await nextP, { value: "a1", done: false });

  await iterator.return(undefined);
});

test("同步可迭代对象也可作为源", async () => {
  const channel = roundRobin([["x1", "x2"], ["y1"]]);
  const out: string[] = [];
  for await (const value of channel) out.push(value);
  assert.deepEqual(out, ["x1", "y1", "x2"]);
});
