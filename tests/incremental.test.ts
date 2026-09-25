import { test } from "node:test";
import assert from "node:assert/strict";
import { TreeModel } from "../src/model.ts";
import type { TreeNode } from "../src/model.ts";
import { layoutRows, rowAtY } from "../src/metrics.ts";
import { metricsFixture, treeFixture } from "../src/fixtures.ts";

/** Independent brute-force visible sequence, used as the oracle. */
function bruteForceVisible(root: TreeNode, model: TreeModel): string[] {
  const out: string[] = [];
  const walk = (node: TreeNode): void => {
    if (!model.isVisibleSelf(node)) return;
    out.push(node.id);
    if (node.expanded) for (const child of node.children) walk(child);
  };
  walk(root);
  return out;
}

function bigFixture(): { model: TreeModel } {
  const children = [];
  for (let i = 0; i < 50; i++) {
    const grand = [];
    for (let j = 0; j < 20; j++) grand.push({ id: `n${i}-${j}`, label: `node ${i}-${j}` });
    children.push({ id: `n${i}`, label: `node ${i}`, children: grand });
  }
  return { model: new TreeModel({ id: "root", label: "root", children }) };
}

test("incremental vc matches the brute-force oracle after every edit", () => {
  const { model } = bigFixture();
  const check = (): void => {
    assert.deepEqual(model.visibleIds(), bruteForceVisible(model.root, model));
    assert.equal(model.totalVisible, bruteForceVisible(model.root, model).length);
  };
  check();
  model.expand("n7");
  check();
  model.expand("n8");
  check();
  model.collapse("n7");
  check();
  model.insert("n8", { id: "fresh", label: "fresh", children: [{ id: "fresh-1", label: "f1" }] }, 5);
  check();
  model.expand("fresh");
  check();
  model.move("n8", "n7", 0);
  check();
  model.remove("fresh");
  check();
  model.setFilter((label) => label.includes("1"));
  check();
  model.setFilter(null);
  check();
});

test("structural edits never trigger a whole-tree recomputation", () => {
  const { model } = bigFixture();
  const baseline = model.stats.fullRecomputes; // 1 from the constructor
  model.expand("n0");
  model.expand("n1");
  model.collapse("n0");
  model.insert("n1", { id: "x", label: "x" });
  model.move("x", "n0", 0);
  model.remove("x");
  model.setDisabled("n2", true);
  assert.equal(model.stats.fullRecomputes, baseline);
  model.setFilter((label) => label.includes("9"));
  assert.equal(model.stats.fullRecomputes, baseline + 1); // filter is the one exception
  model.setFilter(null);
  assert.equal(model.stats.fullRecomputes, baseline + 2);
});

test("indexOf and at are inverses on the visible sequence", () => {
  const { model } = bigFixture();
  model.expand("n3");
  model.expand("n4");
  for (let i = 0; i < model.totalVisible; i++) {
    const node = model.at(i)!;
    assert.equal(model.indexOf(node), i);
  }
  assert.equal(model.at(-1), null);
  assert.equal(model.at(model.totalVisible), null);
});

test("layout comes from fixture metrics only", () => {
  const model = new TreeModel(treeFixture());
  model.expand("src");
  const metrics = metricsFixture();
  const rows = layoutRows(model, metrics, 0, model.totalVisible);
  assert.equal(rows.length, 8);
  const app = rows.find((r) => r.id === "app")!;
  assert.equal(app.y, 2 * metrics.rowHeight);
  assert.equal(app.depth, 2);
  assert.equal(app.labelX, 2 * metrics.indentWidth + metrics.toggleWidth);
  assert.equal(app.labelWidth, "app.ts".length * metrics.charWidth);
  assert.equal(rowAtY(metrics, 2 * metrics.rowHeight + 3, rows.length), 2);
  assert.equal(rowAtY(metrics, 9999, rows.length), -1);
});
