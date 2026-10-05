import { test } from "node:test";
import assert from "node:assert/strict";
import { MessageBus } from "../src/bus.ts";
import { OriginStore } from "../src/origin.ts";
import { CacheNode } from "../src/node.ts";

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

async function flush(times = 5): Promise<void> {
  for (let i = 0; i < times; i++) await tick();
}

/** 等待若干事件循环后，报告 promise 是否已落定（用于断言“读在等待”）。 */
async function isSettled(p: Promise<unknown>): Promise<boolean> {
  let settled = false;
  void p.then(
    () => {
      settled = true;
    },
    () => {
      settled = true;
    },
  );
  await flush();
  return settled;
}

function setup() {
  const bus = new MessageBus();
  const origin = new OriginStore(bus);
  const node = new CacheNode("edge-1", bus, origin);
  return { bus, origin, node };
}

test("屏障发布前允许继续读旧值", async () => {
  const { origin, node } = setup();
  origin.update("k", "v1");
  origin.replicate("k");
  const first = await node.read("k");
  assert.equal(first.value, "v1");
  assert.equal(node.inspect("k").state, "CLEAN");

  origin.update("k", "v2"); // 失效消息已送达，屏障未发布
  assert.equal(node.inspect("k").state, "STALE_ALLOWED");

  const stale = await node.read("k");
  assert.equal(stale.value, "v1");
  assert.equal(stale.source, "cache");
});

test("屏障发布后只能读新版本或等待（失效先于数据到达）", async () => {
  const { origin, node } = setup();
  origin.update("k", "v1");
  origin.replicate("k");
  await node.read("k");

  origin.update("k", "v2"); // 失效消息到达，但 v2 数据尚未复制
  origin.publishBarrier("k");
  assert.equal(node.inspect("k").state, "BARRIER_WAIT");

  const pending = node.read("k");
  assert.equal(await isSettled(pending), false, "屏障后不得返回旧值，只能等待");

  origin.replicate("k"); // v2 数据到达
  const result = await pending;
  assert.equal(result.value, "v2");
  assert.equal(result.version, 2);
});

test("迟到的旧加载结果必须被丢弃，不得写入缓存或被返回", async () => {
  const { origin, node } = setup();
  origin.update("k", "v1");
  origin.replicate("k");

  origin.holdFetches();
  const pending = node.read("k"); // 加载在途（发起时还没有屏障）
  origin.update("k", "v2");
  origin.publishBarrier("k"); // 屏障先于在途加载完成到达
  origin.releaseFetches(); // 在途加载此刻才带着旧值 v1 返回

  await flush();
  assert.equal(node.inspect("k").cachedVersion, 0, "迟到的 v1 不得写入缓存");
  assert.equal(
    await isSettled(pending),
    false,
    "读请求必须等待 v2，而不是返回迟到的 v1",
  );

  origin.replicate("k");
  const result = await pending;
  assert.equal(result.version, 2);
  assert.equal(node.inspect("k").cachedVersion, 2);
});

test("节点重连后补发失效与屏障，不得继续读旧值", async () => {
  const { origin, node } = setup();
  origin.update("k", "v1");
  origin.replicate("k");
  await node.read("k");

  node.disconnect();
  origin.update("k", "v2");
  origin.replicate("k");
  origin.publishBarrier("k"); // 断线期间的消息全部落日志

  node.reconnect(); // 重放失效 + 屏障
  assert.equal(node.inspect("k").state, "BARRIER_WAIT");
  assert.equal(node.inspect("k").barrier, 2);

  const result = await node.read("k");
  assert.equal(result.value, "v2");
  assert.equal(result.version, 2);
});

test("屏障发布后新加入的节点也不得绕过屏障", async () => {
  const bus = new MessageBus();
  const origin = new OriginStore(bus);
  origin.update("k", "v1");
  origin.replicate("k");
  origin.update("k", "v2");
  origin.publishBarrier("k"); // v2 尚未复制

  const late = new CacheNode("edge-late", bus, origin); // 连接时补发历史消息
  assert.equal(late.inspect("k").state, "BARRIER_WAIT");

  const pending = late.read("k");
  assert.equal(await isSettled(pending), false, "只能等待 v2，不得读 v1");
  origin.replicate("k");
  assert.equal((await pending).version, 2);
});

test("连续两次更新与乱序消息不会绕过屏障", async () => {
  const { bus, origin, node } = setup();
  origin.update("k", "v1");
  origin.replicate("k");
  origin.publishBarrier("k");
  const r1 = await node.read("k");
  assert.equal(r1.version, 1);

  // 第二次更新：失效与屏障都到达，但数据未复制
  origin.update("k", "v2");
  origin.publishBarrier("k");
  assert.equal(node.inspect("k").state, "BARRIER_WAIT");

  // 迟到的旧屏障（version 1）不得让状态回退
  origin.publishBarrier("k", 1);
  assert.equal(node.inspect("k").barrier, 2);

  // 读请求只能等待 v2，不得返回满足旧屏障的 v1
  const p2 = node.read("k");
  assert.equal(await isSettled(p2), false);
  origin.replicate("k");
  assert.equal((await p2).version, 2);

  // 第三次更新：乱序投递 —— 屏障先于失效到达
  bus.pause("edge-1");
  origin.update("k", "v3");
  origin.publishBarrier("k");
  assert.equal(bus.deliverNext("edge-1", (m) => m.kind === "barrier"), true);
  assert.equal(node.inspect("k").barrier, 3);
  bus.resume("edge-1"); // 失效消息随后到达
  assert.equal(node.inspect("k").state, "BARRIER_WAIT");

  const p3 = node.read("k");
  assert.equal(await isSettled(p3), false, "v2 不满足屏障 v3，必须等待");
  origin.replicate("k");
  assert.equal((await p3).version, 3);
  assert.equal(node.inspect("k").state, "CLEAN");
});
