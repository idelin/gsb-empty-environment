import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { VirtualList } from "../src/virtual-list.ts";
import type { VirtualItem } from "../src/virtual-list.ts";

function makeItems(count: number): VirtualItem[] {
  return Array.from({ length: count }, (_, index) => ({
    key: `k${index}`,
    content: { index, label: `item-${index}` },
    renderGeneration: 0,
  }));
}

describe("VirtualList 节点回收模型", () => {
  it("滚动时节点槽位稳定、业务键换绑，产生节点复用事件", () => {
    const list = new VirtualList(makeItems(10), { nodeCount: 4 });
    const first = list.snapshot();
    assert.deepEqual(
      first.rows.map((row) => row.nodeId),
      ["node-0", "node-1", "node-2", "node-3"],
    );
    assert.deepEqual(
      first.rows.map((row) => row.key),
      ["k0", "k1", "k2", "k3"],
    );

    list.scrollBy(3);
    const second = list.snapshot();
    assert.deepEqual(
      second.rows.map((row) => row.nodeId),
      ["node-0", "node-1", "node-2", "node-3"],
    );
    assert.deepEqual(
      second.rows.map((row) => row.key),
      ["k3", "k4", "k5", "k6"],
    );

    const rebinds = list
      .bindingLog()
      .filter((event) => event.fromKey !== null && event.toKey !== null);
    // node-0 从 k0 换成 k3，证明同一 DOM 节点被回收承载新业务键。
    const nodeZero = rebinds.filter((event) => event.nodeId === "node-0");
    assert.deepEqual(
      nodeZero.map((event) => [event.fromKey, event.toKey]),
      [["k0", "k3"]],
    );
  });

  it("头部插入后窗口内容整体平移且节点保持复用", () => {
    const list = new VirtualList(makeItems(5), { nodeCount: 3 });
    list.snapshot();
    list.insertAtHead({ key: "k-new", content: { label: "new" }, renderGeneration: 0 });
    const snap = list.snapshot();
    assert.deepEqual(
      snap.rows.map((row) => row.key),
      ["k-new", "k0", "k1"],
    );
    assert.deepEqual(
      snap.rows.map((row) => row.nodeId),
      ["node-0", "node-1", "node-2"],
    );
    assert.equal(list.allKeys().length, 6);
  });

  it("回滚滚动窗口按预期后退，渲染代次随内容更新推进", () => {
    const list = new VirtualList(makeItems(8), { nodeCount: 3 });
    list.snapshot();
    list.scrollBy(4);
    const forward = list.snapshot();
    assert.deepEqual(forward.rows.map((row) => row.key), ["k4", "k5", "k6"]);

    list.scrollBy(-2);
    const back = list.snapshot();
    assert.deepEqual(back.rows.map((row) => row.key), ["k2", "k3", "k4"]);

    list.updateContent("k3", { label: "updated" });
    const updated = list.item("k3");
    assert.equal(updated?.renderGeneration, 1);
    assert.equal((updated?.content as { label: string }).label, "updated");
  });

  it("循环模式越过末尾回到头部", () => {
    const list = new VirtualList(makeItems(5), { nodeCount: 3, circular: true });
    list.scrollToIndex(4);
    const snap = list.snapshot();
    assert.deepEqual(snap.rows.map((row) => row.key), ["k4", "k0", "k1"]);
  });

  it("非循环模式滚动到底部后窗口被钳制，末尾出现空槽", () => {
    const list = new VirtualList(makeItems(4), { nodeCount: 4 });
    list.scrollToIndex(2);
    const snap = list.snapshot();
    assert.deepEqual(snap.rows.map((row) => row.key), ["k2", "k3"]);
  });
});
