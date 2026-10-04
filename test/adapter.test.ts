import { strict as assert } from "assert";
import {
  fromCallbackSource,
  SourceController,
  SourceHandlers,
} from "../src/index";

/** 手动驱动的数据源，记录反压与清理调用。 */
class ManualSource<T> {
  handlers!: SourceHandlers<T>;
  pauseCount = 0;
  resumeCount = 0;
  stopCount = 0;
  paused = false;

  readonly controller: SourceController = {
    pause: () => {
      this.paused = true;
      this.pauseCount += 1;
    },
    resume: () => {
      this.paused = false;
      this.resumeCount += 1;
    },
    stop: () => {
      this.stopCount += 1;
    },
  };

  start = (handlers: SourceHandlers<T>): SourceController => {
    this.handlers = handlers;
    return this.controller;
  };
}

type TestFn = () => void | Promise<void>;
const tests: Array<[string, TestFn]> = [];
function test(name: string, fn: TestFn): void {
  tests.push([name, fn]);
}

test("按到达顺序交付数据，end 后迭代完成，清理恰好一次", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  src.handlers.onData(1);
  src.handlers.onData(2);
  src.handlers.onData(3);
  src.handlers.onEnd();

  const seen: number[] = [];
  for await (const v of it) seen.push(v);
  assert.deepEqual(seen, [1, 2, 3]);
  assert.equal(src.stopCount, 1);
});

test("缓冲满 8 条时触发 pause，消费后触发 resume", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  for (let i = 0; i < 8; i += 1) src.handlers.onData(i);
  assert.equal(src.pauseCount, 1, "第 8 条入缓冲后应 pause");
  assert.equal(src.paused, true);

  const first = await it.next();
  assert.deepEqual(first, { value: 0, done: false });
  assert.equal(src.resumeCount, 1, "缓冲跌破上限后应 resume");
  assert.equal(src.paused, false);
  await it.return!();
});

test("反压全程生效：25 条数据，暂停期间数据源不发送", async () => {
  const total = 25;
  let emitted = 0;
  let paused = false;
  let emittedWhilePaused = 0;
  let stopCount = 0;

  const it = fromCallbackSource<number>((h) => {
    const pump = (): void => {
      while (!paused && emitted < total) {
        if (paused) emittedWhilePaused += 1;
        h.onData(emitted);
        emitted += 1;
      }
      if (emitted >= total) h.onEnd();
    };
    queueMicrotask(pump);
    return {
      pause: () => {
        paused = true;
      },
      resume: () => {
        paused = false;
        queueMicrotask(pump);
      },
      stop: () => {
        stopCount += 1;
      },
    };
  });

  const seen: number[] = [];
  for await (const v of it) seen.push(v);
  assert.deepEqual(seen, Array.from({ length: total }, (_, i) => i));
  assert.equal(emittedWhilePaused, 0, "暂停期间不得有数据发出");
  assert.equal(stopCount, 1);
});

test("错误不插队：缓存数据先于错误交付", async () => {
  const src = new ManualSource<string>();
  const it = fromCallbackSource(src.start);
  const boom = new Error("boom");
  src.handlers.onData("a");
  src.handlers.onData("b");
  src.handlers.onError(boom);

  assert.deepEqual(await it.next(), { value: "a", done: false });
  assert.deepEqual(await it.next(), { value: "b", done: false });
  await assert.rejects(it.next(), (e) => e === boom);
  assert.equal(src.stopCount, 1);
});

test("error 与 end 相邻到达：error 先则 error 胜，后续通知被忽略", async () => {
  const src = new ManualSource<string>();
  const it = fromCallbackSource(src.start);
  const boom = new Error("boom");
  src.handlers.onData("x");
  src.handlers.onError(boom);
  src.handlers.onEnd();
  src.handlers.onData("late");

  assert.deepEqual(await it.next(), { value: "x", done: false });
  await assert.rejects(it.next(), (e) => e === boom);
  await assert.rejects(it.next(), (e) => e === boom, "终止后再次读取仍应得到同一错误");
  assert.equal(src.stopCount, 1);
});

test("error 与 end 相邻到达：end 先则 end 胜，error 被忽略", async () => {
  const src = new ManualSource<string>();
  const it = fromCallbackSource(src.start);
  src.handlers.onData("x");
  src.handlers.onEnd();
  src.handlers.onError(new Error("late error"));
  src.handlers.onData("late");

  assert.deepEqual(await it.next(), { value: "x", done: false });
  assert.deepEqual(await it.next(), { value: undefined, done: true });
  assert.deepEqual(await it.next(), { value: undefined, done: true });
  assert.equal(src.stopCount, 1);
});

test("多个挂起读取：end 到达时全部收到完成通知", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  const p1 = it.next();
  const p2 = it.next();
  const p3 = it.next();
  src.handlers.onEnd();

  const results = await Promise.all([p1, p2, p3]);
  for (const r of results) assert.deepEqual(r, { value: undefined, done: true });
  assert.equal(src.stopCount, 1);
});

test("多个挂起读取：error 到达时全部以同一错误 reject", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  const boom = new Error("boom");
  const p1 = it.next();
  const p2 = it.next();
  const p3 = it.next();
  src.handlers.onError(boom);

  const results = await Promise.allSettled([p1, p2, p3]);
  for (const r of results) {
    assert.equal(r.status, "rejected");
    if (r.status === "rejected") assert.equal(r.reason, boom);
  }
  assert.equal(src.stopCount, 1);
});

test("多个挂起读取：数据按序交付给等待者，终止通知不遗漏", async () => {
  const src = new ManualSource<string>();
  const it = fromCallbackSource(src.start);
  const p1 = it.next();
  const p2 = it.next();
  const p3 = it.next();
  src.handlers.onData("a");
  src.handlers.onData("b");
  src.handlers.onEnd();

  const [r1, r2, r3] = await Promise.all([p1, p2, p3]);
  assert.deepEqual(r1, { value: "a", done: false });
  assert.deepEqual(r2, { value: "b", done: false });
  assert.deepEqual(r3, { value: undefined, done: true });
  assert.equal(src.stopCount, 1);
});

test("挂起读取期间 error 到达：缓存数据先交付，剩余读者收到错误", async () => {
  const src = new ManualSource<string>();
  const it = fromCallbackSource(src.start);
  const p1 = it.next();
  const p2 = it.next();
  const boom = new Error("boom");
  src.handlers.onData("a");
  src.handlers.onError(boom);

  assert.deepEqual(await p1, { value: "a", done: false });
  await assert.rejects(p2, (e) => e === boom);
  assert.equal(src.stopCount, 1);
});

test("消费者提前 return：挂起读取收到 done，清理恰好一次，迟到事件被忽略", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  src.handlers.onData(1);
  const pending = it.next(); // 消耗掉缓存的 1
  const late1 = it.next(); // 挂起
  const late2 = it.next(); // 挂起

  assert.deepEqual(await pending, { value: 1, done: false });
  await it.return!();

  assert.deepEqual(await late1, { value: undefined, done: true });
  assert.deepEqual(await late2, { value: undefined, done: true });
  assert.equal(src.stopCount, 1);

  // 终止后数据源再发事件不得影响迭代器
  src.handlers.onData(99);
  src.handlers.onError(new Error("late"));
  src.handlers.onEnd();
  assert.deepEqual(await it.next(), { value: undefined, done: true });
  assert.equal(src.stopCount, 1);
});

test("for-await 提前 break 触发清理且仅一次", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  for (let i = 0; i < 5; i += 1) src.handlers.onData(i);

  for await (const v of it) {
    assert.equal(v, 0);
    break;
  }
  assert.equal(src.stopCount, 1);
  await it.return!();
  assert.equal(src.stopCount, 1, "重复 return 不得重复清理");
});

test("同步数据源：start 期间同步发数据并结束", async () => {
  let stopCount = 0;
  const it = fromCallbackSource<number>((h) => {
    h.onData(1);
    h.onData(2);
    h.onEnd();
    return {
      stop: () => {
        stopCount += 1;
      },
    };
  });

  const seen: number[] = [];
  for await (const v of it) seen.push(v);
  assert.deepEqual(seen, [1, 2]);
  assert.equal(stopCount, 1);
});

test("同步数据源：start 期间同步出错", async () => {
  let stopCount = 0;
  const boom = new Error("sync boom");
  const it = fromCallbackSource<number>((h) => {
    h.onData(1);
    h.onError(boom);
    return {
      stop: () => {
        stopCount += 1;
      },
    };
  });

  assert.deepEqual(await it.next(), { value: 1, done: false });
  await assert.rejects(it.next(), (e) => e === boom);
  assert.equal(stopCount, 1);
});

test("end 与 return 都发生时清理仍恰好一次", async () => {
  const src = new ManualSource<number>();
  const it = fromCallbackSource(src.start);
  src.handlers.onData(1);
  src.handlers.onEnd();
  await it.return!();
  assert.equal(src.stopCount, 1);
  assert.deepEqual(await it.next(), { value: undefined, done: true });
  assert.equal(src.stopCount, 1);
});

test("非法 bufferSize 直接抛错", () => {
  assert.throws(() => fromCallbackSource(() => {}, { bufferSize: 0 }), RangeError);
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok - ${name}`);
    } catch (err) {
      failed += 1;
      console.error(`FAIL - ${name}`);
      console.error(err);
    }
  }
  console.log(`\n${tests.length - failed}/${tests.length} tests passed`);
  if (failed > 0) process.exit(1);
})();
