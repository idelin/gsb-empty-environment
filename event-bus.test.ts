import { test } from "node:test";
import assert from "node:assert/strict";
import { EventBus } from "./event-bus.ts";

type E = {
  a: number;
  b: number;
  x: string;
};

test("按注册顺序逐个调用，emit 返回空错误数组", () => {
  const bus = new EventBus<E>();
  const order: string[] = [];
  bus.on("a", () => void order.push("first"));
  bus.on("a", () => void order.push("second"));
  bus.on("a", () => void order.push("third"));
  const errors = bus.emit("a", 1);
  assert.deepEqual(order, ["first", "second", "third"]);
  assert.deepEqual(errors, []);
});

test("无监听器时 emit 返回空数组", () => {
  const bus = new EventBus<E>();
  assert.deepEqual(bus.emit("a", 1), []);
});

test("本轮新注册的订阅从下一次 emit 生效（快照语义）", () => {
  const bus = new EventBus<E>();
  const calls: string[] = [];
  bus.on("a", () => {
    calls.push("A");
    bus.on("a", () => void calls.push("C")); // 本轮注册，本轮不生效
  });
  bus.on("a", () => void calls.push("B"));

  bus.emit("a", 1);
  assert.deepEqual(calls, ["A", "B"]);

  bus.emit("a", 2);
  assert.deepEqual(calls, ["A", "B", "A", "B", "C"]);
});

test("监听器中互相注销：A 注销排在其后的 B，B 本轮被跳过", () => {
  const bus = new EventBus<E>();
  const calls: string[] = [];
  let subB!: { dispose(): void };
  bus.on("a", () => {
    calls.push("A");
    subB.dispose();
  });
  subB = bus.on("a", () => void calls.push("B"));
  bus.on("a", () => void calls.push("C"));

  bus.emit("a", 1);
  assert.deepEqual(calls, ["A", "C"]); // B 被跳过，C 不受影响
});

test("监听器中自我注销，后续轮次不再触发", () => {
  const bus = new EventBus<E>();
  let count = 0;
  const sub = bus.on("a", () => {
    count++;
    sub.dispose();
  });
  bus.emit("a", 1);
  bus.emit("a", 2);
  assert.equal(count, 1);
  assert.equal(sub.disposed, true);
});

test("同步嵌套 emit（重入）：内层 emit 插队执行，外层继续", () => {
  const bus = new EventBus<E>();
  const order: string[] = [];
  bus.on("a", () => {
    order.push("a1");
    bus.emit("b", 0);
    order.push("a1-after");
  });
  bus.on("a", () => void order.push("a2"));
  bus.on("b", () => void order.push("b1"));

  bus.emit("a", 1);
  assert.deepEqual(order, ["a1", "b1", "a1-after", "a2"]);
});

test("once 递归：处理器内重入 emit 同一事件不会再次触发", () => {
  const bus = new EventBus<E>();
  let count = 0;
  bus.once("a", () => {
    count++;
    bus.emit("a", 0); // 递归重入：once 已在调用前移除，不会无限递归
  });
  bus.emit("a", 1);
  assert.equal(count, 1);
  assert.equal(bus.size, 0);
});

test("once 只触发一次，且异常时也已在调用前移除", () => {
  const bus = new EventBus<E>();
  let count = 0;
  bus.once("a", () => {
    count++;
    throw new Error("boom");
  });
  const errors1 = bus.emit("a", 1);
  const errors2 = bus.emit("a", 2);
  assert.equal(count, 1);
  assert.equal(errors1.length, 1);
  assert.deepEqual(errors2, []);
});

test("异常单独收集返回：不吞掉、不阻断其他监听器", () => {
  const bus = new EventBus<E>();
  const err1 = new Error("boom-1");
  const err2 = new Error("boom-2");
  const calls: string[] = [];
  bus.on("a", () => {
    throw err1;
  });
  bus.on("a", () => void calls.push("ok"));
  bus.on("a", () => {
    throw err2;
  });

  const errors = bus.emit("a", 1);
  assert.deepEqual(errors, [err1, err2]); // 原样返回，未包装未吞掉
  assert.deepEqual(calls, ["ok"]); // 中间监听器正常执行
});

test("重复 dispose 幂等：计数只减一次", () => {
  const bus = new EventBus<E>();
  const sub = bus.on("a", () => {});
  assert.equal(bus.size, 1);
  sub.dispose();
  sub.dispose();
  sub.dispose();
  assert.equal(sub.disposed, true);
  assert.equal(bus.size, 0);
  assert.equal(bus.listenerCount("a"), 0);
});

test("AbortSignal：abort 后订阅失效，不再触发且计数归零", () => {
  const bus = new EventBus<E>();
  const controller = new AbortController();
  let count = 0;
  const sub = bus.on("a", () => void count++, { signal: controller.signal });
  bus.emit("a", 1);
  assert.equal(count, 1);

  controller.abort();
  assert.equal(sub.disposed, true);
  assert.equal(bus.size, 0);
  bus.emit("a", 2);
  assert.equal(count, 1);
});

test("AbortSignal 已取消：订阅立即得到已 dispose 的空句柄", () => {
  const bus = new EventBus<E>();
  const controller = new AbortController();
  controller.abort(); // 订阅前就取消

  let count = 0;
  const sub = bus.on("a", () => void count++, { signal: controller.signal });
  assert.equal(sub.disposed, true);
  assert.equal(bus.size, 0); // 不产生计数
  bus.emit("a", 1);
  assert.equal(count, 0); // 从不触发
});

test("emit 本轮中 abort 排在后面的订阅：该订阅被跳过", () => {
  const bus = new EventBus<E>();
  const controller = new AbortController();
  const calls: string[] = [];
  bus.on("a", () => {
    calls.push("A");
    controller.abort();
  });
  bus.on("a", () => void calls.push("B"), { signal: controller.signal });

  bus.emit("a", 1);
  assert.deepEqual(calls, ["A"]);
  assert.equal(bus.size, 1); // 只剩未关联 signal 的 A
});

test("按 scope 注销：close 批量移除，其余订阅不受影响", () => {
  const bus = new EventBus<E>();
  const scope = bus.createScope();
  const calls: string[] = [];
  bus.on("a", () => void calls.push("scoped-a"), { scope });
  bus.on("b", () => void calls.push("scoped-b"), { scope });
  bus.on("a", () => void calls.push("global"));

  assert.equal(bus.size, 3);
  assert.equal(scope.size, 2);

  scope.close();
  assert.equal(scope.closed, true);
  assert.equal(scope.size, 0);
  assert.equal(bus.size, 1);

  bus.emit("a", 1);
  bus.emit("b", 1);
  assert.deepEqual(calls, ["global"]);
});

test("scope 关闭后拒绝新订阅", () => {
  const bus = new EventBus<E>();
  const scope = bus.createScope();
  scope.close();
  assert.throws(() => bus.on("a", () => {}, { scope }), /scope is closed/);
  assert.equal(bus.size, 0);
  // 未关联该 scope 的订阅不受影响
  bus.on("a", () => {});
  assert.equal(bus.size, 1);
});

test("scope.close 幂等，可重复调用", () => {
  const bus = new EventBus<E>();
  const scope = bus.createScope();
  bus.on("a", () => {}, { scope });
  scope.close();
  scope.close();
  assert.equal(bus.size, 0);
});

test("循环创建销毁 scope 后计数归零（不依赖 GC）", () => {
  const bus = new EventBus<E>();
  for (let i = 0; i < 10_000; i++) {
    const scope = bus.createScope();
    const controller = new AbortController();
    bus.on("a", () => {}, { scope });
    bus.once("a", () => {}, { scope });
    bus.on("b", () => {}, { scope, signal: controller.signal });
    scope.close();
    controller.abort(); // 已 dispose 的订阅上重复 abort 也应无害
  }
  assert.equal(bus.size, 0);
  assert.equal(bus.listenerCount("a"), 0);
  assert.equal(bus.listenerCount("b"), 0);
});

test("弱引用订阅：存活目标正常触发，sweep 不误删", () => {
  const bus = new EventBus<E>();
  const calls: string[] = [];
  const handler = () => void calls.push("weak"); // 测试自己持有强引用
  bus.on("a", handler, { weak: true });

  bus.emit("a", 1);
  assert.deepEqual(calls, ["weak"]);
  assert.equal(bus.sweep(), 0); // 目标存活，不清扫
  assert.equal(bus.size, 1);
});

test("弱引用订阅仍可被 dispose 可靠释放", () => {
  const bus = new EventBus<E>();
  let count = 0;
  const handler = () => void count++;
  const sub = bus.on("a", handler, { weak: true });
  sub.dispose();
  assert.equal(bus.size, 0);
  bus.emit("a", 1);
  assert.equal(count, 0);
});

test("组合：scope + once + signal 同时关联", () => {
  const bus = new EventBus<E>();
  const scope = bus.createScope();
  const controller = new AbortController();
  let count = 0;
  bus.once("a", () => void count++, { scope, signal: controller.signal });
  assert.equal(bus.size, 1);
  bus.emit("a", 1);
  assert.equal(count, 1);
  assert.equal(bus.size, 0); // once 触发后自动注销
  scope.close();
  controller.abort();
  assert.equal(bus.size, 0);
});
