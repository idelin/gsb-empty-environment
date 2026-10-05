/**
 * 共享操作日志：纯数据，可在任意数量的 Store 实例上原样回放。
 * 覆盖：互相依赖的双分片、通知中重入提交、回调抛错、回调中取消订阅、
 * 分支提交、快照固定/释放、容量回收。
 */
import type { Op } from "./harness.ts";

export const INITIAL_STATE = {
  cart: { items: [{ id: "a", qty: 1 }] },
  inventory: { a: 5 },
};

export const CAPACITY = 12;

export const OP_LOG: Op[] = [
  // 订阅者 A：首次通知时重入提交一个事务；B：每轮抛错；C：首轮取消 D。
  { op: "subscribe", id: "A", spec: { recommitOnce: { path: ["cart", "stamp"], value: "from-A" } } },
  { op: "subscribe", id: "B", spec: { throwOnCall: true } },
  { op: "subscribe", id: "C", spec: { unsubscribeOnFirst: ["D"] } },
  { op: "subscribe", id: "D" },

  // v2：购物车 + 库存同一事务原子提交（互相依赖的两个分片）。
  { op: "begin", tx: "t1" },
  { op: "set", tx: "t1", path: ["cart", "items", 0, "qty"], value: 3 },
  { op: "set", tx: "t1", path: ["inventory", "a"], value: 2 },
  { op: "commit", tx: "t1" },
  // A 的重入提交产生 v3；D 已被 C 取消，收不到任何通知。

  // 快照固定 v2。
  { op: "checkout", id: "s2", version: 2 },

  // v4：改库存，触发容量回收（v1 未固定被逐出，v2 被快照固定保留）。
  { op: "begin", tx: "t2" },
  { op: "set", tx: "t2", path: ["inventory", "b"], value: 8 },
  { op: "commit", tx: "t2" },

  // 从固定的 v2 开分支：购物车与库存一起回到 v2 视角，再叠加新写入。
  { op: "begin", tx: "t3", from: 2 },
  { op: "set", tx: "t3", path: ["cart", "items"], value: [{ id: "b", qty: 7 }] },
  { op: "delete", tx: "t3", path: ["inventory", "a"] },
  { op: "commit", tx: "t3" }, // v5，父版本是 2

  // 释放快照；下一次提交产生容量压力，v2 随之被回收。
  { op: "release", id: "s2" },
  { op: "begin", tx: "t4" },
  { op: "set", tx: "t4", path: ["inventory", "a"], value: 0 },
  { op: "set", tx: "t4", path: ["cart", "items", 0, "qty"], value: 8 },
  { op: "commit", tx: "t4" }, // v6

  // 中止的事务不产生版本。
  { op: "begin", tx: "t5" },
  { op: "set", tx: "t5", path: ["cart", "junk"], value: true },
  { op: "abort", tx: "t5" },

  // 最终检出头部核对状态。
  { op: "checkout", id: "head", version: 6 },
];
