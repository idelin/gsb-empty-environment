import { test } from "node:test";
import assert from "node:assert/strict";

import {
  SimNavigationObserver,
  SimTransport,
  SimWindow,
  TaskQueue,
  TrustedMessageReceiver,
  type TrustedRequest,
  type WindowProxyLike,
} from "../index.ts";

const CHILD_ORIGIN = "https://child.example";
const HOST_ORIGIN = "https://host.example";
const ATTACKER_ORIGIN = "https://attacker.example";
const SECRET = { secret: "host-only-secret" };

/** 多轮排空：兼顾任务队列与 await 链上的微任务。 */
async function settle(queue: TaskQueue, rounds = 4): Promise<void> {
  for (let i = 0; i < rounds; i++) {
    await queue.drain();
  }
}

interface Harness {
  queue: TaskQueue;
  host: SimWindow;
  child: SimWindow;
  transport: SimTransport;
  observer: SimNavigationObserver;
}

function makeHarness(): Harness {
  const queue = new TaskQueue();
  const host = new SimWindow("host", HOST_ORIGIN);
  const child = new SimWindow("child", CHILD_ORIGIN);
  const transport = new SimTransport(queue);
  const observer = new SimNavigationObserver();
  observer.watch(child, host);
  return { queue, host, child, transport, observer };
}

test("基线：未导航时回复到达原始文档，代次与来源被固化", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let seen: TrustedRequest | null = null;
  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: (req) => {
      seen = req;
      req.reply(SECRET);
    },
  });

  transport.postMessage(child, host, { question: 1 });
  await settle(queue);

  assert.equal(seen?.origin, CHILD_ORIGIN);
  assert.equal(seen?.generation, 0);
  assert.equal(seen?.source, child);
  assert.deepEqual(child.mailboxForGeneration(0), [SECRET]);
  assert.equal(receiver.stats.repliesDelivered, 1);
  assert.equal(receiver.stats.repliesDenied, 0);

  // 之后导航：新文档邮箱为空，旧文档的回复不会"转移"给新文档。
  child.navigate(CHILD_ORIGIN);
  assert.deepEqual(child.currentMailbox(), []);
  assert.deepEqual(child.mailboxForGeneration(0), [SECRET]);
  receiver.dispose();
});

test("核心竞争：异步校验期间发生同源导航，回复被拒绝且不泄漏给新文档", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let releaseValidation = (): void => {};
  const validationGate = new Promise<void>((resolve) => {
    releaseValidation = resolve;
  });
  let outcome: { ok: boolean; reason?: string } | null = null;
  let seen: TrustedRequest | null = null;

  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: async (req) => {
      seen = req;
      await validationGate; // 模拟耗时的来源/签名校验
      outcome = req.reply(SECRET);
    },
  });

  transport.postMessage(child, host, { question: 2 });
  await settle(queue);
  assert.equal(seen?.generation, 0);

  // 校验挂起期间，发送窗口导航到另一个同源文档（同一 WindowProxy 复用）。
  child.navigate(CHILD_ORIGIN);
  assert.equal(child.generation, 1);

  releaseValidation();
  await settle(queue);

  assert.equal(outcome?.ok, false);
  assert.equal(outcome?.reason, "window-navigated");
  // 旧文档已销毁、新文档未收到任何内容。
  assert.deepEqual(child.mailboxForGeneration(0), []);
  assert.deepEqual(child.currentMailbox(), []);
  assert.equal(receiver.stats.revokedByNavigation, 1);
  assert.equal(receiver.stats.repliesDelivered, 0);
  assert.equal(receiver.stats.repliesDenied, 1);
  receiver.dispose();
});

test("跨源导航同样拒绝回复，攻击者文档收不到任何数据", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let releaseValidation = (): void => {};
  const gate = new Promise<void>((r) => (releaseValidation = r));
  let outcome: { ok: boolean; reason?: string } | null = null;

  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: async (req) => {
      await gate;
      outcome = req.reply(SECRET);
    },
  });

  transport.postMessage(child, host, {});
  await settle(queue);
  child.navigate(ATTACKER_ORIGIN);
  releaseValidation();
  await settle(queue);

  assert.equal(outcome?.ok, false);
  assert.deepEqual(child.currentMailbox(), []);
  assert.deepEqual(child.mailboxForGeneration(0), []);
  assert.equal(child.droppedByTargetOrigin, 0); // 组件在传输层之前就拒绝了
  assert.equal(receiver.stats.repliesDelivered, 0);
  receiver.dispose();
});

test("窗口对象复用不继承能力：新文档获得新能力，旧能力对新文档无效", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let releaseAll = (): void => {};
  const gate = new Promise<void>((r) => (releaseAll = r));
  const requests: TrustedRequest[] = [];
  const outcomes: Array<{ ok: boolean; reason?: string }> = [];

  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: async (req) => {
      requests.push(req);
      await gate;
      outcomes.push(req.reply(SECRET));
    },
  });

  // 旧文档（gen 0）发消息。
  transport.postMessage(child, host, { phase: "old" });
  await settle(queue);

  // 导航到同源新文档（gen 1），WindowProxy 是同一个对象。
  child.navigate(CHILD_ORIGIN);

  // 新文档复用同一窗口对象发消息。
  transport.postMessage(child, host, { phase: "new" });
  await settle(queue);

  assert.equal(requests.length, 2);
  assert.equal(requests[0].source, requests[1].source); // 同一窗口对象
  assert.equal(requests[0].generation, 0);
  assert.equal(requests[1].generation, 1); // 能力按代次区分

  releaseAll();
  await settle(queue);

  assert.equal(outcomes.length, 2);
  assert.deepEqual(outcomes[0], { ok: false, reason: "window-navigated" });
  assert.deepEqual(outcomes[1], { ok: true, deliveredToGeneration: 1 });
  // 旧文档无回复；新文档只收到属于自己的那一条，拿不到旧文档的任何内容。
  assert.deepEqual(child.mailboxForGeneration(0), []);
  assert.deepEqual(child.currentMailbox(), [SECRET]);
  assert.equal(receiver.stats.revokedByNavigation, 1);
  assert.equal(receiver.stats.repliesDelivered, 1);
  assert.equal(receiver.stats.repliesDenied, 1);
  receiver.dispose();
});

test("在途消息遇到导航：事件代次过期，直接拒绝且不建立能力", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  const handlerCalls: string[] = [];
  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: (req) => handlerCalls.push(req.data as string),
  });

  // 消息入队但尚未投递，窗口先导航。
  transport.postMessage(child, host, "in-flight");
  child.navigate(CHILD_ORIGIN);
  await settle(queue);

  assert.deepEqual(handlerCalls, []);
  assert.equal(receiver.stats.received, 1);
  assert.equal(receiver.stats.rejectedStale, 1);
  assert.equal(receiver.stats.accepted, 0);
  assert.deepEqual(child.currentMailbox(), []);
  receiver.dispose();
});

test("来源白名单：非允许来源的消息被拒绝，处理器不执行", async () => {
  const { queue, host, transport, observer } = makeHarness();
  const evil = new SimWindow("evil", ATTACKER_ORIGIN);
  observer.watch(evil);
  const handlerCalls: unknown[] = [];
  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: (req) => handlerCalls.push(req.data),
  });

  transport.postMessage(evil, host, { ping: true });
  await settle(queue);

  assert.deepEqual(handlerCalls, []);
  assert.equal(receiver.stats.rejectedOrigin, 1);
  assert.equal(receiver.stats.accepted, 0);
  receiver.dispose();
});

test("能力一次性：重复回复被阻止，防止重放", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let reqRef: TrustedRequest | null = null;
  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: (req) => {
      reqRef = req;
    },
  });

  transport.postMessage(child, host, {});
  await settle(queue);
  const first = reqRef!.reply(SECRET);
  const second = reqRef!.reply(SECRET);
  await settle(queue);

  assert.deepEqual(first, { ok: true, deliveredToGeneration: 0 });
  assert.deepEqual(second, { ok: false, reason: "capability-consumed" });
  assert.deepEqual(child.mailboxForGeneration(0), [SECRET]);
  assert.equal(receiver.stats.repliesDelivered, 1);
  assert.equal(receiver.stats.repliesDenied, 1);
  receiver.dispose();
});

test("可信元数据不可变：篡改请求对象不影响内部固化的来源与代次", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let reqRef: TrustedRequest | null = null;
  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: (req) => {
      reqRef = req;
    },
  });

  transport.postMessage(child, host, {});
  await settle(queue);

  const mutable = reqRef as unknown as {
    origin: string;
    generation: number;
  };
  mutable.origin = ATTACKER_ORIGIN;
  mutable.generation = 999;
  const outcome = reqRef!.reply(SECRET);
  await settle(queue);

  assert.deepEqual(outcome, { ok: true, deliveredToGeneration: 0 });
  // 回复仍按内部固化的 CHILD_ORIGIN 投递，外部篡改无效。
  assert.deepEqual(child.mailboxForGeneration(0), [SECRET]);
  assert.equal(child.droppedByTargetOrigin, 0);
  receiver.dispose();
});

test("dispose 后所有能力失效", async () => {
  const { queue, host, child, transport, observer } = makeHarness();
  let reqRef: TrustedRequest | null = null;
  const receiver = new TrustedMessageReceiver({
    transport,
    observeNavigation: observer.observe,
    allowedOrigins: [CHILD_ORIGIN],
    handler: (req) => {
      reqRef = req;
    },
  });
  transport.postMessage(child, host, {});
  await settle(queue);
  receiver.dispose();

  const outcome = reqRef!.reply(SECRET);
  assert.equal(outcome.ok, false);
  assert.deepEqual(child.currentMailbox(), []);
});

test("构造配置拒绝通配与不透明来源", () => {
  const { transport, observer } = makeHarness();
  assert.throws(
    () =>
      new TrustedMessageReceiver({
        transport,
        observeNavigation: observer.observe,
        allowedOrigins: ["*"],
        handler: () => {},
      }),
    /not allowed/,
  );
  assert.throws(
    () =>
      new TrustedMessageReceiver({
        transport,
        observeNavigation: observer.observe,
        allowedOrigins: [],
        handler: () => {},
      }),
    /must not be empty/,
  );
});

// ---------------------------------------------------------------------------
// 红队对照：朴素实现（只校验 origin + 缓存 source）在导航竞争下会泄漏。
// 这些测试不使用 TrustedMessageReceiver，用于证明本组件要解决的真实缺口。
// ---------------------------------------------------------------------------

test("红队对照：朴素缓存在同源导航后把响应泄漏给新文档", async () => {
  const { queue, host, child, transport } = makeHarness();
  let cachedSource: WindowProxyLike | null = null;
  let cachedOrigin = "";
  transport.listen((event) => {
    // 朴素做法：只验 origin，然后缓存 source 留待异步回复。
    if (event.origin !== CHILD_ORIGIN) return;
    cachedSource = event.source;
    cachedOrigin = event.origin;
  });

  transport.postMessage(child, host, { question: "naive" });
  await settle(queue);

  // 异步"校验"期间同源导航，WindowProxy 被新文档复用。
  child.navigate(CHILD_ORIGIN);

  // 朴素回复：targetOrigin 与新文档同源，浏览器不会拦截 -> 泄漏。
  transport.post(cachedSource!, SECRET, cachedOrigin);
  await settle(queue);

  assert.equal(child.generation, 1);
  assert.deepEqual(child.currentMailbox(), [SECRET]); // 新文档拿到了敏感响应
  assert.equal(child.droppedByTargetOrigin, 0);
});

test("红队对照：targetOrigin 只能拦跨源导航，无法替代代次绑定", async () => {
  const { queue, host, child, transport } = makeHarness();
  let cachedSource: WindowProxyLike | null = null;
  let cachedOrigin = "";
  transport.listen((event) => {
    if (event.origin !== CHILD_ORIGIN) return;
    cachedSource = event.source;
    cachedOrigin = event.origin;
  });

  transport.postMessage(child, host, { question: "cross-origin" });
  await settle(queue);

  // 跨源导航：新文档是攻击者来源。
  child.navigate(ATTACKER_ORIGIN);
  transport.post(cachedSource!, SECRET, cachedOrigin);
  await settle(queue);

  // targetOrigin 挡住了跨源情况（消息被静默丢弃），
  // 但同源导航（上一个用例）无法被挡住 -> 必须依赖文档代次绑定。
  assert.deepEqual(child.currentMailbox(), []);
  assert.equal(child.droppedByTargetOrigin, 1);
});
