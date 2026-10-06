import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FenwickTree } from "../src/fenwick.ts";
import { ChatListLayout } from "../src/layout.ts";

describe("FenwickTree", () => {
  it("computes prefix sums and totals", () => {
    const tree = FenwickTree.from([10, 20, 30, 40]);
    assert.equal(tree.sum(0), 0);
    assert.equal(tree.sum(2), 30);
    assert.equal(tree.sum(4), 100);
    assert.equal(tree.total(), 100);
  });

  it("applies point updates", () => {
    const tree = FenwickTree.from([10, 20, 30]);
    tree.add(1, 5);
    tree.add(0, -10);
    assert.equal(tree.sum(1), 0);
    assert.equal(tree.sum(3), 55);
  });

  it("finds the largest prefix count not exceeding a value", () => {
    const tree = FenwickTree.from([10, 20, 30]);
    assert.equal(tree.maxPrefixLeq(0), 0);
    assert.equal(tree.maxPrefixLeq(9), 0);
    assert.equal(tree.maxPrefixLeq(10), 1);
    assert.equal(tree.maxPrefixLeq(29), 1);
    assert.equal(tree.maxPrefixLeq(30), 2);
    assert.equal(tree.maxPrefixLeq(60), 3);
    assert.equal(tree.maxPrefixLeq(1000), 3);
  });

  it("handles zero-height entries", () => {
    const tree = FenwickTree.from([0, 0, 5]);
    assert.equal(tree.maxPrefixLeq(0), 2);
    assert.equal(tree.maxPrefixLeq(4), 2);
    assert.equal(tree.maxPrefixLeq(5), 3);
  });
});

describe("ChatListLayout", () => {
  it("uses the estimate until items are measured", () => {
    const layout = new ChatListLayout({
      estimatedHeight: 50,
      items: [{ key: "a" }, { key: "b", height: 80 }, { key: "c" }],
    });
    assert.equal(layout.count, 3);
    assert.equal(layout.totalHeight(), 180);
    assert.equal(layout.topOf(0), 0);
    assert.equal(layout.topOf(1), 50);
    assert.equal(layout.topOf(2), 130);
    assert.equal(layout.bottomOf(2), 180);
  });

  it("applies height corrections", () => {
    const layout = new ChatListLayout({ estimatedHeight: 50, items: [{ key: "a" }, { key: "b" }] });
    assert.equal(layout.measure("a", 120), 70);
    assert.equal(layout.totalHeight(), 170);
    assert.equal(layout.topOf(1), 120);
    assert.equal(layout.measure("a", 120), 0);
    assert.equal(layout.measure("missing", 10), 0);
  });

  it("prepends and inserts, shifting tops", () => {
    const layout = new ChatListLayout({ items: [{ key: "c", height: 30 }] });
    layout.prepend([{ key: "a", height: 10 }]);
    layout.insert(1, [{ key: "b", height: 20 }]);
    assert.deepEqual([0, 1, 2].map((i) => layout.keyAt(i)), ["a", "b", "c"]);
    assert.deepEqual([0, 1, 2, 3].map((i) => layout.topOf(i)), [0, 10, 30, 60]);
  });

  it("removes items by key and by range", () => {
    const layout = new ChatListLayout({
      items: [
        { key: "a", height: 10 },
        { key: "b", height: 20 },
        { key: "c", height: 30 },
        { key: "d", height: 40 },
      ],
    });
    assert.deepEqual(layout.removeKeys(["b", "missing"]), ["b"]);
    assert.equal(layout.totalHeight(), 80);
    assert.deepEqual(layout.removeAt(1, 2), ["c", "d"]);
    assert.deepEqual([...Array(layout.count).keys()].map((i) => layout.keyAt(i)), ["a"]);
    assert.equal(layout.totalHeight(), 10);
  });

  it("rejects duplicate keys", () => {
    const layout = new ChatListLayout({ items: [{ key: "a" }] });
    assert.throws(() => layout.append([{ key: "a" }]), /duplicate key/);
  });

  describe("locate", () => {
    const layout = new ChatListLayout({
      items: [
        { key: "a", height: 100 },
        { key: "b", height: 100 },
        { key: "c", height: 100 },
      ],
    });

    it("assigns boundary offsets to the item starting there", () => {
      const at = layout.locate(100)!;
      assert.equal(at.key, "b");
      assert.equal(at.offsetInItem, 0);
    });

    it("locates offsets inside items", () => {
      const at = layout.locate(250)!;
      assert.equal(at.index, 2);
      assert.equal(at.top, 200);
      assert.equal(at.offsetInItem, 50);
    });

    it("clamps out-of-range offsets", () => {
      assert.equal(layout.locate(-5)!.key, "a");
      const end = layout.locate(10_000)!;
      assert.equal(end.key, "c");
      assert.equal(end.offsetInItem, 100);
    });

    it("returns null when empty", () => {
      assert.equal(new ChatListLayout().locate(0), null);
    });

    it("skips zero-height items", () => {
      const zeroed = new ChatListLayout({
        items: [
          { key: "z", height: 0 },
          { key: "n", height: 10 },
        ],
      });
      assert.equal(zeroed.locate(0)!.key, "n");
    });
  });
});
