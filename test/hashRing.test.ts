import { test } from "node:test";
import assert from "node:assert/strict";
import { HashRing } from "../src/hashRing.ts";

test("同一 key 路由结果确定且稳定", () => {
  const ring = new HashRing();
  ring.addNode("node-a");
  ring.addNode("node-b");
  ring.addNode("node-c");
  const first = ring.route("device:ABC");
  for (let i = 0; i < 100; i++) {
    assert.equal(ring.route("device:ABC"), first);
  }
});

test("扩容时仅少量 key 迁移，且迁移的 key 全部流向新节点", () => {
  const ring = new HashRing();
  ring.addNode("n1");
  ring.addNode("n2");
  ring.addNode("n3");

  const keys = Array.from({ length: 20000 }, (_, i) => `device:MAC-${i}`);
  const before = new Map(keys.map((k) => [k, ring.route(k)]));

  ring.addNode("n4");
  let moved = 0;
  for (const k of keys) {
    const after = ring.route(k);
    if (after !== before.get(k)) {
      moved++;
      assert.equal(after, "n4", "一致性哈希：变动的 key 只能迁移到新节点");
    }
  }
  const ratio = moved / keys.length;
  assert.ok(ratio > 0.05 && ratio < 0.5, `迁移比例应接近 1/4，实际 ${(ratio * 100).toFixed(1)}%`);
});

test("摘除节点（容灾）时仅受影响 key 迁移到存活节点", () => {
  const ring = new HashRing();
  ring.addNode("n1");
  ring.addNode("n2");
  ring.addNode("n3");

  const keys = Array.from({ length: 20000 }, (_, i) => `device:MAC-${i}`);
  const before = new Map(keys.map((k) => [k, ring.route(k)]));

  ring.removeNode("n2");
  for (const k of keys) {
    const prev = before.get(k)!;
    const after = ring.route(k);
    if (prev === "n2") {
      assert.notEqual(after, "n2", "故障节点上的 key 必须迁走");
      assert.ok(after === "n1" || after === "n3");
    } else {
      assert.equal(after, prev, "未受影响节点上的 key 不得迁移");
    }
  }
});
