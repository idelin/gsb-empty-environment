import { test } from "node:test";
import assert from "node:assert/strict";
import { DualRingController } from "../src/controller.ts";
import type { RingNode } from "../src/ring.ts";

const ZONES = ["z1", "z2", "z3"];

function makeNodes(count: number): RingNode[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `node-${i}`,
    topology: { region: "cn-east", zone: ZONES[i % ZONES.length] },
  }));
}

function makeKeys(count: number): string[] {
  return Array.from({ length: count }, (_, i) => `key-${i}`);
}

function setup(keyCount = 5000) {
  const controller = new DualRingController(makeNodes(6));
  const keys = makeKeys(keyCount);
  controller.trackKeys(keys);
  return { controller, keys };
}

test("主环承载全量流量，路由确定性且节点均存活", () => {
  const { controller, keys } = setup();
  const alive = new Set(makeNodes(6).map((n) => n.id));
  for (const key of keys) {
    const decision = controller.route(key);
    assert.equal(decision.role, "active");
    assert.equal(decision.epoch, 0);
    assert.ok(decision.nodeId !== undefined);
    assert.ok(alive.has(decision.nodeId!));
    assert.deepEqual(controller.route(key), decision);
  }
});

test("备环注入缩容变更期间主环路由零扰动", () => {
  const { controller, keys } = setup();
  const before = new Map(keys.map((k) => [k, controller.route(k).nodeId]));

  const report = controller.injectTopologyChange({ remove: ["node-1", "node-4"] });
  assert.equal(report.status, "ready");
  assert.equal(controller.warmupStatus, "ready");

  for (const key of keys) {
    assert.equal(controller.route(key).nodeId, before.get(key));
    assert.equal(controller.route(key).epoch, 0, "epoch must not bump before failover");
  }
});

test("预热阶段预先计算键迁移影响且估算合理", () => {
  const { controller } = setup();
  const report = controller.injectTopologyChange({ remove: ["node-1", "node-4"] });
  const plan = report.plan;

  assert.equal(plan.trackedKeys, 5000);
  assert.ok(plan.movedKeys > 0);
  // 缩容 2/6 节点，理论上约 1/3 的键迁移
  assert.ok(
    plan.estimatedMovedFraction > 0.25 && plan.estimatedMovedFraction < 0.42,
    `estimated fraction ${plan.estimatedMovedFraction} should be ~1/3`,
  );
  const trackedRatio = plan.movedKeys / plan.trackedKeys;
  assert.ok(
    Math.abs(trackedRatio - plan.estimatedMovedFraction) < 0.05,
    `tracked ratio ${trackedRatio} should match estimate`,
  );
  // 迁移明细自洽：from 必须是被摘除节点，to 必须不是
  for (const m of plan.migrations) {
    assert.ok(m.from === "node-1" || m.from === "node-4");
    assert.ok(m.to !== "node-1" && m.to !== "node-4");
  }
  // 每个被摘除节点净减少量等于其迁出键数
  const lost =
    -(plan.perNodeDelta["node-1"] ?? 0) - (plan.perNodeDelta["node-4"] ?? 0);
  assert.equal(lost, plan.movedKeys);
});

test("未预热完成时禁止翻转", () => {
  const { controller } = setup();
  assert.throws(() => controller.failover(), /not ready/);
});

test("验收场景：稳态 → 备环缩容预热 → 原子翻转 → 零瞬时路由错乱", () => {
  const { controller, keys } = setup();

  // 1. 主环平稳运行，记录全量流量快照
  const steadySnapshot = new Map(keys.map((k) => [k, controller.route(k)]));

  // 2. 向备环注入缩容变更并预热
  const warmup = controller.injectTopologyChange({ remove: ["node-1", "node-4"] });
  assert.equal(warmup.status, "ready");
  const plan = warmup.plan;

  // 3. 预热期间主环流量无任何扰动
  for (const key of keys) {
    assert.deepEqual(controller.route(key), steadySnapshot.get(key));
  }

  // 4. 原子指针翻转，毫秒级（实际亚毫秒）完成
  const failover = controller.failover();
  assert.ok(failover.flipMs < 10, `flip took ${failover.flipMs}ms, must be ms-level`);
  assert.equal(failover.epoch, 1);
  assert.equal(failover.nodeCount, 4);

  // 5. 切换后全部路由与预热预案逐键一致：零瞬时路由错乱
  const verification = controller.verifyAgainstPlan(plan);
  assert.equal(verification.checked, keys.length);
  assert.deepEqual(verification.mismatches, []);

  // 6. 无任何键落到已摘除节点；迁移键集合与预案完全吻合
  let actuallyMoved = 0;
  for (const key of keys) {
    const now = controller.route(key);
    assert.equal(now.epoch, 1);
    assert.ok(now.nodeId !== "node-1" && now.nodeId !== "node-4");
    if (now.nodeId !== steadySnapshot.get(key)!.nodeId) actuallyMoved++;
  }
  assert.equal(actuallyMoved, plan.movedKeys);
});

test("翻转是原子的：翻转前后不存在中间混合态", () => {
  const { controller, keys } = setup();
  controller.injectTopologyChange({ remove: ["node-2"] });

  const epochsSeen = new Set<number>();
  const before = controller.route(keys[0]);
  epochsSeen.add(before.epoch);
  controller.failover();
  const after = controller.route(keys[0]);
  epochsSeen.add(after.epoch);

  assert.deepEqual([...epochsSeen].sort(), [0, 1]);
  // 同一世代内路由结果必然一致（单环语义）
  for (const key of keys.slice(0, 200)) {
    assert.equal(controller.route(key).epoch, 1);
  }
});

test("翻转后备环回退为旧主环，可再次注入变更", () => {
  const { controller, keys } = setup();
  controller.injectTopologyChange({ remove: ["node-5"] });
  controller.failover();

  const report = controller.injectTopologyChange({
    add: [{ id: "node-6", topology: { region: "cn-east", zone: "z1" } }],
  });
  assert.equal(report.status, "ready");
  controller.failover();

  for (const key of keys) {
    const decision = controller.route(key);
    assert.equal(decision.epoch, 2);
    assert.ok(decision.nodeId !== "node-5");
  }
});

test("迁移预案在翻转后清空，状态回到 idle", () => {
  const { controller } = setup();
  controller.injectTopologyChange({ remove: ["node-0"] });
  assert.ok(controller.getMigrationPlan() !== null);
  controller.failover();
  assert.equal(controller.getMigrationPlan(), null);
  assert.equal(controller.warmupStatus, "idle");
});
