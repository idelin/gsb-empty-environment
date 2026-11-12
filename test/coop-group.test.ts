import test from "node:test";
import assert from "node:assert/strict";
import {
  CoopGroupManager,
  isCompatible,
  type CompatibilityMatrix,
} from "../index.ts";

const ORIGIN_A = "https://a.example";
const ORIGIN_B = "https://b.example";

const matrix: CompatibilityMatrix = {
  "unsafe-none": { "unsafe-none": true },
  "same-origin": { "same-origin": true },
  "same-origin-allow-popups": {
    "same-origin-allow-popups": true,
    "unsafe-none": { crossOrigin: true },
  },
};

function buildChain(): CoopGroupManager {
  const manager = new CoopGroupManager(matrix);
  manager.createWindow("A", { origin: ORIGIN_A, policy: "unsafe-none" });
  manager.createWindow("B", { origin: ORIGIN_A, policy: "unsafe-none" }, "A");
  manager.createWindow("C", { origin: ORIGIN_A, policy: "unsafe-none" }, "B");
  return manager;
}

test("兼容矩阵判定:同源需规则允许,跨源需显式 crossOrigin", () => {
  const sameA = { origin: ORIGIN_A, policy: "same-origin" };
  const sameB = { origin: ORIGIN_B, policy: "same-origin" };
  assert.equal(isCompatible(sameA, sameA, matrix), true);
  assert.equal(isCompatible(sameA, sameB, matrix), false);
  assert.equal(
    isCompatible(
      { origin: ORIGIN_A, policy: "same-origin-allow-popups" },
      { origin: ORIGIN_B, policy: "unsafe-none" },
      matrix,
    ),
    true,
  );
  assert.equal(
    isCompatible(
      { origin: ORIGIN_A, policy: "unsafe-none" },
      { origin: ORIGIN_B, policy: "same-origin" },
      matrix,
    ),
    false,
  );
});

test("仅兼容时保留组:opener 兼容才同组,导航不兼容则切组", () => {
  const manager = new CoopGroupManager(matrix);
  manager.createWindow("A", { origin: ORIGIN_A, policy: "unsafe-none" });
  manager.createWindow("B", { origin: ORIGIN_A, policy: "unsafe-none" }, "A");
  assert.equal(manager.groupOf("A"), manager.groupOf("B"));
  assert.equal(manager.canAccess("A", "B"), true);
  assert.equal(manager.canAccess("B", "A"), true);

  manager.createWindow("D", { origin: ORIGIN_A, policy: "same-origin" }, "A");
  assert.notEqual(manager.groupOf("D"), manager.groupOf("A"));
  assert.equal(manager.canAccess("A", "D"), false);
  assert.equal(manager.canAccess("D", "A"), false);

  const kept = manager.navigate("B", { origin: ORIGIN_A, policy: "unsafe-none" });
  assert.equal(kept.keptGroup, true);
  assert.equal(manager.groupOf("B"), manager.groupOf("A"));

  const switched = manager.navigate("B", { origin: ORIGIN_A, policy: "same-origin" });
  assert.equal(switched.keptGroup, false);
  assert.notEqual(manager.groupOf("B"), manager.groupOf("A"));
  assert.equal(manager.canAccess("A", "B"), false);
});

test("切组撤销旧跨窗口能力,已接收消息保持归属", () => {
  const manager = buildChain();
  const groupBefore = manager.groupOf("B");

  const delivered = manager.postMessage("B", "A", { hello: 1 });
  assert.ok(delivered);
  assert.equal(delivered.groupId, groupBefore);

  manager.navigate("B", { origin: ORIGIN_B, policy: "unsafe-none" });
  assert.equal(manager.canAccess("A", "B"), false);
  assert.equal(manager.canAccess("B", "A"), false);
  assert.equal(manager.canAccess("B", "C"), false);
  assert.equal(manager.canAccess("C", "B"), false);
  assert.equal(manager.postMessage("B", "A", { hello: 2 }), null);

  assert.equal(manager.messages.length, 1);
  assert.equal(manager.messages[0], delivered);
  assert.equal(manager.messages[0].groupId, groupBefore);
});

test("多窗口链 + 前进后退:迟到回调不恢复已切断关系", () => {
  const manager = buildChain();
  const groupBefore = manager.groupOf("B");
  assert.equal(manager.groupOf("A"), groupBefore);
  assert.equal(manager.groupOf("C"), groupBefore);

  manager.postMessage("A", "B", "ping");
  manager.defer(() => manager.postMessage("B", "A", "late-pong"));
  manager.defer(() => manager.canAccess("B", "C"));

  manager.navigate("B", { origin: ORIGIN_B, policy: "unsafe-none" });
  assert.deepEqual(manager.linksOf("B"), []);

  const back = manager.navigate(
    "B",
    { origin: ORIGIN_A, policy: "unsafe-none" },
    "back",
  );
  assert.equal(back.keptGroup, true);
  assert.notEqual(manager.groupOf("B"), groupBefore);

  const forward = manager.navigate(
    "B",
    { origin: ORIGIN_B, policy: "unsafe-none" },
    "forward",
  );
  assert.equal(forward.keptGroup, true);
  manager.navigate(
    "B",
    { origin: ORIGIN_A, policy: "unsafe-none" },
    "back",
  );

  const results = manager.flush();
  assert.equal(results[0], null);
  assert.equal(results[1], false);

  assert.equal(manager.canAccess("A", "B"), false);
  assert.equal(manager.canAccess("B", "A"), false);
  assert.equal(manager.canAccess("B", "C"), false);
  assert.deepEqual(manager.linksOf("B"), []);
  assert.notEqual(manager.groupOf("B"), manager.groupOf("A"));
  assert.notEqual(manager.groupOf("B"), manager.groupOf("C"));

  assert.equal(manager.messages.length, 1);
  assert.equal(manager.messages[0].payload, "ping");
  assert.equal(manager.messages[0].groupId, groupBefore);
});
