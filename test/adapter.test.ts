import { test } from "node:test";
import assert from "node:assert/strict";
import {
  fromCallbackSource,
  type CallbackSource,
  type SourceHandlers,
} from "../src/index.ts";

/** 可手动驱动的数据源，记录反压与清理调用次数。 */
class ManualSource<T> implements CallbackSource<T> {
  handlers!: SourceHandlers<T>;
  pauseCount = 0;
  resumeCount = 0;
  disposeCount = 0;

  start(handlers: SourceHandlers<T>): void {
    this.handlers = handlers;
  }
  pause(): void {
    this.pauseCount++;
  }
  resume(): void {
    this.resumeCount++;
  }
  dispose(): void {
    this.disposeCount++;
  }

  push(value: T): void {
    this.handlers.onData(value);
  }
  error(error: unknown): void {
    this.handlers.onError(error);
  }
  end(): void {
    this.handlers.onEnd();
  }
}

test("按序交付数据并以 done 结束", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);
  source.push(1);
  source.push(2);
  source.push(3);
  source.end();

  const seen: number[] = [];
  for await (const value of it) seen.push(value);
  assert.deepEqual(seen, [1, 2, 3]);
  assert.equal(source.disposeCount, 1);
});

test("反压：缓冲满 8 条时 pause，消费后 resume", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);

  for (let i = 0; i < 8; i++) source.push(i);
  assert.equal(source.pauseCount, 1, "第 8 条入缓冲时触发 pause");

  source.push(8); // 不合作的数据源多推一条，不应重复 pause
  assert.equal(source.pauseCount, 1);

  assert.deepEqual(await it.next(), { value: 0, done: false });
  assert.equal(source.resumeCount, 0, "缓冲仍有 8 条，未降到上限以下");
  assert.deepEqual(await it.next(), { value: 1, done: false });
  assert.equal(source.resumeCount, 1, "缓冲降到上限以下时触发 resume");

  source.end();
  const rest: number[] = [];
  for await (const value of it) rest.push(value);
  assert.deepEqual(rest, [2, 3, 4, 5, 6, 7, 8]);
});

test("协作式数据源下缓冲不超过 8 条", async () => {
  const source = new ManualSource<number>();
  let paused = false;
  source.pause = () => {
    paused = true;
    source.pauseCount++;
  };
  source.resume = () => {
    paused = false;
    source.resumeCount++;
  };

  const it = fromCallbackSource(source);
  let pushed = 0;
  // 模拟数据源持续产出，但遵守 pause 信号。
  while (pushed < 20 && !paused) source.push(pushed++);
  assert.equal(pushed, 8, "pause 后合作源停止推送，缓冲恰好 8 条");

  const seen: number[] = [];
  for await (const value of it) {
    seen.push(value);
    while (pushed < 20 && !paused) source.push(pushed++);
    if (pushed >= 20) source.end();
  }
  assert.deepEqual(seen, Array.from({ length: 20 }, (_, i) => i));
  assert.ok(source.pauseCount >= 1);
  assert.ok(source.resumeCount >= 1);
});

test("消费者提前结束：清理恰好一次，后续读取返回 done", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);
  for (let i = 0; i < 5; i++) source.push(i);

  const seen: number[] = [];
  for await (const value of it) {
    seen.push(value);
    if (seen.length === 2) break; // 提前结束，触发 return()
  }
  assert.deepEqual(seen, [0, 1]);
  assert.equal(source.disposeCount, 1);

  // 清理后数据源残余信号全部忽略，且不会重复清理。
  source.push(99);
  source.end();
  source.error(new Error("late"));
  assert.equal(source.disposeCount, 1);
  assert.deepEqual(await it.next(), { value: undefined, done: true });
});

test("唯一清理：终止信号与 return() 竞争时 dispose 只调一次", async () => {
  const byError = new ManualSource<number>();
  const it1 = fromCallbackSource(byError);
  byError.error(new Error("boom"));
  await it1.return();
  assert.equal(byError.disposeCount, 1);

  const byEnd = new ManualSource<number>();
  const it2 = fromCallbackSource(byEnd);
  byEnd.end();
  await it2.return();
  await it2.return();
  assert.equal(byEnd.disposeCount, 1);
});

test("错误与结束相邻到达：先到的终止信号生效", async () => {
  // error 先到：缓冲数据交付完后抛错，end 被忽略。
  const errorFirst = new ManualSource<number>();
  const it1 = fromCallbackSource(errorFirst);
  errorFirst.push(1);
  errorFirst.push(2);
  const boom = new Error("boom");
  errorFirst.error(boom);
  errorFirst.end();

  assert.deepEqual(await it1.next(), { value: 1, done: false });
  assert.deepEqual(await it1.next(), { value: 2, done: false });
  await assert.rejects(it1.next(), (err) => err === boom);
  assert.equal(errorFirst.disposeCount, 1);

  // end 先到：缓冲数据交付完后 done，error 被忽略。
  const endFirst = new ManualSource<number>();
  const it2 = fromCallbackSource(endFirst);
  endFirst.push(1);
  endFirst.end();
  endFirst.error(new Error("late"));

  assert.deepEqual(await it2.next(), { value: 1, done: false });
  assert.deepEqual(await it2.next(), { value: undefined, done: true });
  assert.equal(endFirst.disposeCount, 1);
});

test("交付顺序：错误到达时缓冲中的数据先交付", async () => {
  const source = new ManualSource<string>();
  const it = fromCallbackSource(source);
  source.push("a");
  source.push("b");
  source.push("c");
  const boom = new Error("boom");
  source.error(boom);

  const seen: string[] = [];
  await assert.rejects(async () => {
    for await (const value of it) seen.push(value);
  }, (err) => err === boom);
  assert.deepEqual(seen, ["a", "b", "c"], "错误之前已缓冲的数据全部按序交付");
});

test("多个挂起读取：错误终止广播给所有挂起者", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);

  const p1 = it.next();
  const p2 = it.next();
  const p3 = it.next();

  const boom = new Error("boom");
  source.error(boom);

  const results = await Promise.allSettled([p1, p2, p3]);
  for (const result of results) {
    assert.equal(result.status, "rejected");
    assert.equal((result as PromiseRejectedResult).reason, boom);
  }
  assert.equal(source.disposeCount, 1);
});

test("多个挂起读取：结束终止广播给所有挂起者", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);

  const p1 = it.next();
  const p2 = it.next();
  source.end();

  assert.deepEqual(await p1, { value: undefined, done: true });
  assert.deepEqual(await p2, { value: undefined, done: true });
});

test("多个挂起读取：数据先到先得，剩余挂起者收到终止通知", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);

  const p1 = it.next();
  const p2 = it.next();
  const p3 = it.next();

  source.push(10);
  source.push(20);
  source.end(); // 缓冲已空，第三个挂起者必须收到 done，不能漏。

  assert.deepEqual(await p1, { value: 10, done: false });
  assert.deepEqual(await p2, { value: 20, done: false });
  assert.deepEqual(await p3, { value: undefined, done: true });
});

test("挂起读取期间消费者提前结束：挂起者全部以 done 收场", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);

  const p1 = it.next();
  const p2 = it.next();
  await it.return();

  assert.deepEqual(await p1, { value: undefined, done: true });
  assert.deepEqual(await p2, { value: undefined, done: true });
  assert.equal(source.disposeCount, 1);
});

test("终止之后到达的数据被忽略", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);
  source.push(1);
  source.end();
  source.push(2);

  assert.deepEqual(await it.next(), { value: 1, done: false });
  assert.deepEqual(await it.next(), { value: undefined, done: true });
});

test("start 同步抛错转为错误终止", async () => {
  const boom = new Error("start failed");
  const source: CallbackSource<number> = {
    start(): void {
      throw boom;
    },
  };
  const it = fromCallbackSource(source);
  await assert.rejects(it.next(), (err) => err === boom);
});

test("错误终止是粘性的：缓冲排空后后续 next 仍抛同一错误", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source);
  const boom = new Error("boom");
  source.error(boom);

  await assert.rejects(it.next(), (err) => err === boom);
  await assert.rejects(it.next(), (err) => err === boom);
});

test("自定义缓冲上限", async () => {
  const source = new ManualSource<number>();
  const it = fromCallbackSource(source, { highWaterMark: 3 });
  source.push(1);
  source.push(2);
  assert.equal(source.pauseCount, 0);
  source.push(3);
  assert.equal(source.pauseCount, 1);
  await it.next();
  assert.equal(source.resumeCount, 1);
});

test("非法缓冲上限抛 RangeError", () => {
  const source = new ManualSource<number>();
  assert.throws(() => fromCallbackSource(source, { highWaterMark: 0 }), RangeError);
});
