import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { IncrementalExtractor, VirtualList, stableHash } from "../src/index.ts";
import type { ScrollSnapshot, VirtualItem } from "../src/index.ts";

function makeItems(count: number): VirtualItem[] {
  return Array.from({ length: count }, (_, index) => ({
    key: `k${index}`,
    content: { index, label: `item-${index}` },
    renderGeneration: 0,
  }));
}

/** 逐页向下滚动直到触底，返回每轮 ingest 结果。 */
function traverse(
  list: VirtualList,
  extractor: IncrementalExtractor,
  markEndOnLast = true,
) {
  const results = [];
  let guard = 0;
  while (guard < 1000) {
    const atBottom = list.topIndex + 4 >= list.length;
    if (atBottom && markEndOnLast) list.markEnd(true);
    results.push(extractor.ingest(list.snapshot()));
    if (atBottom) break;
    list.scrollBy(2);
    guard += 1;
  }
  return results;
}

describe("IncrementalExtractor 节点复用与身份", () => {
  it("节点被回收换键时，按业务键新建记录而非复用旧身份", () => {
    const list = new VirtualList(makeItems(10), { nodeCount: 4 });
    const extractor = new IncrementalExtractor();

    const first = extractor.ingest(list.snapshot());
    assert.equal(first.changes.filter((change) => change.kind === "inserted").length, 4);
    assert.deepEqual(first.visibleKeys, ["k0", "k1", "k2", "k3"]);

    list.scrollBy(4);
    const second = extractor.ingest(list.snapshot());
    // node-0 从 k0 换到 k3：k3 是新键（inserted），同时报告节点换绑。
    const k4 = second.changes.find((change) => change.key === "k4");
    assert.equal(k4?.kind, "inserted");
    assert.equal(k4?.nodeId, "node-0");
    assert.ok(
      second.nodeRebinds.some(
        (rebind) =>
          rebind.nodeId === "node-0" &&
          rebind.fromKey === "k0" &&
          rebind.toKey === "k4",
      ),
    );

    // 旧键 k0 不再可见，但记录仍被保留。
    assert.equal(extractor.getRecord("k0")?.visible, false);
    assert.ok(extractor.getRecord("k0"));
    assert.equal(extractor.size, 8);
  });

  it("同键内容变化输出 revised，并保留全部版本与渲染代次", () => {
    const list = new VirtualList(makeItems(3), { nodeCount: 3 });
    const extractor = new IncrementalExtractor();
    extractor.ingest(list.snapshot());

    list.updateContent("k1", { label: "item-1-v2" });
    const result = extractor.ingest(list.snapshot());
    const revision = result.changes.find((change) => change.key === "k1");
    assert.equal(revision?.kind, "revised");
    assert.equal(revision?.renderGeneration, 1);
    assert.notEqual(revision?.previousContentHash, revision?.contentHash);

    const record = extractor.getRecord("k1");
    assert.equal(record?.versions.length, 2);
    assert.deepEqual(
      record?.versions.map((version) => version.renderGeneration),
      [0, 1],
    );
  });

  it("同键同内容不产生修订，即使承载节点已更换", () => {
    const list = new VirtualList(makeItems(10), { nodeCount: 4 });
    const extractor = new IncrementalExtractor();
    extractor.ingest(list.snapshot());
    list.scrollBy(3);
    const result = extractor.ingest(list.snapshot());
    // k3 首次出现；k4..k6 首次出现，没有任何 revised。
    assert.ok(!result.changes.some((change) => change.kind === "revised"));
  });

  it("对象键顺序不同但逻辑内容一致时不产生修订", () => {
    const extractor = new IncrementalExtractor();
    extractor.ingest({
      round: 0,
      rows: [
        { nodeId: "n0", key: "a", content: { x: 1, y: 2 }, renderGeneration: 0 },
      ],
      endMarked: false,
    });
    const reordered = extractor.ingest({
      round: 1,
      rows: [
        { nodeId: "n1", key: "a", content: { y: 2, x: 1 }, renderGeneration: 1 },
      ],
      endMarked: false,
    });
    assert.ok(!reordered.changes.some((change) => change.kind === "revised"));
    assert.equal(extractor.getRecord("a")?.versions.length, 1);
  });

  it("节点换键后旧业务键保留且转为不可见，新键独立建册", () => {
    const extractor = new IncrementalExtractor();
    extractor.ingest({
      round: 0,
      rows: [
        { nodeId: "n0", key: "a", content: { v: 1 }, renderGeneration: 0 },
        { nodeId: "n1", key: "b", content: { v: 2 }, renderGeneration: 0 },
      ],
      endMarked: false,
    });
    const rebound = extractor.ingest({
      round: 1,
      rows: [
        { nodeId: "n0", key: "c", content: { v: 3 }, renderGeneration: 0 },
        { nodeId: "n1", key: "b", content: { v: 2 }, renderGeneration: 0 },
      ],
      endMarked: false,
    });
    assert.deepEqual(
      rebound.nodeRebinds,
      [{ nodeId: "n0", fromKey: "a", toKey: "c" }],
    );
    assert.equal(extractor.size, 3);
    assert.equal(extractor.getRecord("a")?.visible, false);
    assert.equal(extractor.getRecord("c")?.firstSeenNodeId, "n0");
    assert.equal(extractor.getRecord("b")?.visible, true);
    assert.deepEqual(rebound.hiddenKeys, ["a"]);
  });
});

describe("IncrementalExtractor 缺失行与出现区间", () => {
  it("缺失行仅表示不可见，记录不删除且出现区间被关闭/重开", () => {
    const list = new VirtualList(makeItems(8), { nodeCount: 3 });
    const extractor = new IncrementalExtractor();

    extractor.ingest(list.snapshot()); // k0,k1,k2
    list.scrollBy(4);
    const moved = extractor.ingest(list.snapshot()); // k4,k5,k6
    assert.deepEqual(moved.hiddenKeys.sort(), ["k0", "k1", "k2"]);
    assert.equal(extractor.size, 6);

    list.scrollBy(-4);
    const back = extractor.ingest(list.snapshot()); // k0,k1,k2 回归
    const reseenKeys = back.changes
      .filter((change) => change.kind === "reseen")
      .map((change) => change.key)
      .sort();
    assert.deepEqual(reseenKeys, ["k0", "k1", "k2"]);
    assert.equal(extractor.size, 6);

    const k0 = extractor.getRecord("k0");
    assert.equal(k0?.appearances.length, 2);
    assert.deepEqual(
      k0?.appearances.map((interval) => [interval.fromRound, interval.toRound]),
      [[0, 1], [2, null]],
    );
  });

  it("整表滚动采集不漏不重，每个键恰好 inserted 一次", () => {
    const list = new VirtualList(makeItems(20), { nodeCount: 4 });
    const extractor = new IncrementalExtractor();
    const results = traverse(list, extractor);

    assert.equal(extractor.size, 20);
    const insertedKeys = results.flatMap((result) =>
      result.changes.filter((change) => change.kind === "inserted").map((change) => change.key),
    );
    assert.deepEqual(insertedKeys.sort(), list.allKeys().slice().sort());
    assert.equal(new Set(insertedKeys).size, 20);

    for (const key of list.allKeys()) {
      const record = extractor.getRecord(key);
      assert.ok(record, `记录 ${String(key)} 不应缺失`);
      assert.equal(record?.versions.length, 1);
      assert.ok((record?.appearances.length ?? 0) >= 1);
    }
  });
});

describe("IncrementalExtractor 头部插入", () => {
  it("头部插入新键被采集，且不会被误判为循环回绕", () => {
    const list = new VirtualList(makeItems(6), { nodeCount: 3 });
    const extractor = new IncrementalExtractor();
    extractor.ingest(list.snapshot()); // k0,k1,k2

    list.insertAtHead({ key: "k-new", content: { label: "new" }, renderGeneration: 0 });
    const result = extractor.ingest(list.snapshot()); // k-new,k0,k1
    const inserted = result.changes.find((change) => change.key === "k-new");
    assert.equal(inserted?.kind, "inserted");
    assert.equal(result.incompleteEvidence?.reasons.includes("wrap-seam"), false);
    assert.equal(extractor.size, 4);
  });
});

describe("IncrementalExtractor 完成判定", () => {
  it("只有结束标记且连续两轮边界键不变才完成", () => {
    const list = new VirtualList(makeItems(8), { nodeCount: 3 });
    const extractor = new IncrementalExtractor();

    list.markEnd(true);
    extractor.ingest(list.snapshot()); // round 0：标记出现，边界 k0..k2
    list.scrollBy(2);
    const atEndFirst = extractor.ingest(list.snapshot()); // round 1：标记与新边界首次出现
    assert.equal(atEndFirst.complete, false);
    assert.ok(atEndFirst.incompleteEvidence?.reasons.includes("boundary-changed"));

    // 停在底部再采两轮：边界需连续两轮不变，同时标记保持连续。
    extractor.ingest(list.snapshot()); // round 2：边界稳定 1 轮
    const stable = extractor.ingest(list.snapshot()); // round 3：边界稳定 2 轮，完成
    assert.equal(stable.complete, true);
    assert.equal(stable.completionEvidence?.boundaryStableStreak, 3);
    assert.equal(stable.completionEvidence?.endMarkedStreak, 4);
    assert.equal(stable.completionEvidence?.firstKey, "k2");
    assert.equal(stable.completionEvidence?.lastKey, "k4");
    assert.equal(extractor.completed, true);
  });

  it("结束标记出现但边界仍在移动时不完成", () => {
    const list = new VirtualList(makeItems(8), { nodeCount: 3 });
    const extractor = new IncrementalExtractor();
    list.markEnd(true);
    extractor.ingest(list.snapshot());
    list.scrollBy(2);
    const moved = extractor.ingest(list.snapshot());
    assert.equal(moved.complete, false);
    assert.ok(moved.incompleteEvidence?.reasons.includes("boundary-changed"));
    assert.equal(moved.completionEvidence, null);
  });

  it("边界稳定但缺少结束标记时不完成", () => {
    const extractor = new IncrementalExtractor();
    const rows = (round: number): ScrollSnapshot["rows"] => [
      { nodeId: "n0", key: "a", content: { v: 1 }, renderGeneration: 0 },
      { nodeId: "n1", key: "b", content: { v: 2 }, renderGeneration: 0 },
    ];
    const first = extractor.ingest({ round: 0, rows: rows(0), endMarked: false });
    const second = extractor.ingest({ round: 1, rows: rows(1), endMarked: false });
    assert.equal(first.complete, false);
    assert.equal(second.complete, false);
    assert.ok(second.incompleteEvidence?.reasons.includes("end-not-marked"));
  });

  it("结束标记中断后需重新累计连续轮次", () => {
    const extractor = new IncrementalExtractor();
    const rows: ScrollSnapshot["rows"] = [
      { nodeId: "n0", key: "a", content: { v: 1 }, renderGeneration: 0 },
    ];
    extractor.ingest({ round: 0, rows, endMarked: true });
    const interrupted = extractor.ingest({ round: 1, rows, endMarked: false });
    assert.equal(interrupted.complete, false);
    extractor.ingest({ round: 2, rows, endMarked: true });
    const recovered = extractor.ingest({ round: 3, rows, endMarked: true });
    assert.equal(recovered.complete, true);
    assert.equal(recovered.completionEvidence?.endMarkedStreak, 2);
  });
});

describe("IncrementalExtractor 循环滚动", () => {
  it("循环列表回绕接缝被识别并持续返回未完成证据", () => {
    const list = new VirtualList(makeItems(5), { nodeCount: 3, circular: true });
    const extractor = new IncrementalExtractor();

    // 先在非回绕窗口完成全部键的建册，避免接缝两侧都被当成新键。
    extractor.ingest(list.snapshot()); // k0,k1,k2
    list.scrollBy(2);
    extractor.ingest(list.snapshot()); // k2,k3,k4
    assert.equal(extractor.size, 5);

    list.scrollToIndex(4);
    const wrapped = extractor.ingest(list.snapshot()); // k4,k0,k1
    assert.equal(wrapped.complete, false);
    assert.ok(wrapped.incompleteEvidence?.reasons.includes("wrap-seam"));
    assert.deepEqual(wrapped.incompleteEvidence?.wrapSeam, {
      before: "k4",
      after: "k0",
    });

    // 继续循环滚动多轮，永远不完成，且全部键只采集一次。
    for (let index = 0; index < 6; index += 1) {
      list.scrollBy(2);
      const result = extractor.ingest(list.snapshot());
      assert.equal(result.complete, false);
    }
    assert.equal(extractor.size, 5);
    assert.equal(extractor.completed, false);
  });

  it("循环回到历史边界时报告 boundary-revisit 证据", () => {
    const list = new VirtualList(makeItems(6), { nodeCount: 3, circular: true });
    const extractor = new IncrementalExtractor();
    list.markEnd(true);

    extractor.ingest(list.snapshot()); // 边界 k0..k2，round 0
    list.scrollToIndex(3);
    extractor.ingest(list.snapshot()); // k3..k5，round 1
    list.scrollToIndex(0);
    const revisited = extractor.ingest(list.snapshot()); // 再次 k0..k2，round 2

    assert.ok(revisited.incompleteEvidence?.reasons.includes("boundary-revisit"));
    assert.equal(revisited.incompleteEvidence?.revisitedAtRound, 0);
    assert.equal(revisited.complete, false);
  });
});

describe("IncrementalExtractor 工具行为", () => {
  it("stableHash 对键序不敏感、对内容变化敏感", () => {
    assert.equal(stableHash({ a: 1, b: 2 }), stableHash({ b: 2, a: 1 }));
    assert.notEqual(stableHash({ a: 1, b: 2 }), stableHash({ a: 1, b: 3 }));
    assert.equal(stableHash([1, 2, 3]), stableHash([1, 2, 3]));
    assert.notEqual(stableHash([1, 2, 3]), stableHash([3, 2, 1]));
  });

  it("空快照不产出变更，缺失不删除任何记录", () => {
    const extractor = new IncrementalExtractor();
    extractor.ingest({
      round: 0,
      rows: [{ nodeId: "n0", key: "a", content: { v: 1 }, renderGeneration: 0 }],
      endMarked: false,
    });
    const empty = extractor.ingest({ round: 1, rows: [], endMarked: false });
    assert.deepEqual(empty.changes, []);
    assert.deepEqual(empty.hiddenKeys, ["a"]);
    assert.equal(extractor.size, 1);
    assert.equal(extractor.getRecord("a")?.visible, false);
  });

  it("同一轮节点绑定多个键时抛出数据错误", () => {
    const extractor = new IncrementalExtractor();
    assert.throws(() =>
      extractor.ingest({
        round: 0,
        rows: [
          { nodeId: "n0", key: "a", content: {}, renderGeneration: 0 },
          { nodeId: "n0", key: "b", content: {}, renderGeneration: 0 },
        ],
        endMarked: false,
      }),
    );
  });

  it("同一轮业务键重复时抛出数据错误", () => {
    const extractor = new IncrementalExtractor();
    assert.throws(() =>
      extractor.ingest({
        round: 0,
        rows: [
          { nodeId: "n0", key: "a", content: {}, renderGeneration: 0 },
          { nodeId: "n1", key: "a", content: {}, renderGeneration: 0 },
        ],
        endMarked: false,
      }),
    );
  });
});
