import { test } from "node:test";
import assert from "node:assert/strict";
import { TreeModel } from "../src/model.ts";
import { TreeView } from "../src/view.ts";
import { treeFixture } from "../src/fixtures.ts";

function makeView(): TreeView {
  return new TreeView(new TreeModel(treeFixture()));
}

test("expand/collapse drives the visible sequence", () => {
  const view = makeView();
  const model = view.model;
  assert.deepEqual(model.visibleIds(), ["root", "src", "docs", "scripts", "readme"]);
  model.expand("src");
  assert.deepEqual(model.visibleIds(), [
    "root", "src", "app", "util", "lib", "docs", "scripts", "readme",
  ]);
  model.collapse("src");
  assert.deepEqual(model.visibleIds(), ["root", "src", "docs", "scripts", "readme"]);
});

test("up/down skips disabled nodes; home/end land on enabled rows", () => {
  const view = makeView();
  const model = view.model;
  model.expand("src");
  model.expand("scripts");
  view.home();
  assert.equal(view.focus(), "root");
  view.moveDown();
  assert.equal(view.focus(), "src");
  view.moveDown();
  assert.equal(view.focus(), "app");
  view.moveDown(); // util is disabled -> skipped
  assert.equal(view.focus(), "lib");
  view.moveDown();
  assert.equal(view.focus(), "docs");
  view.moveDown(); // scripts is disabled -> skipped to its enabled child
  assert.equal(view.focus(), "build");
  view.end();
  assert.equal(view.focus(), "readme");
  view.moveUp();
  assert.equal(view.focus(), "clean");
  view.moveUp();
  assert.equal(view.focus(), "build");
});

test("collapsing a subtree that contains the focus moves focus to the parent", () => {
  const view = makeView();
  const model = view.model;
  model.expand("src");
  view.setFocus("app");
  assert.equal(view.focus(), "app");
  view.collapseNode(model.get("src")!);
  assert.equal(view.focus(), "src");
  assert.deepEqual(model.visibleIds(), ["root", "src", "docs", "scripts", "readme"]);
});

test("collapsing via a disabled ancestor falls back to nearest enabled ancestor", () => {
  const view = makeView();
  const model = view.model;
  model.expand("scripts");
  view.setFocus("build");
  view.collapseNode(model.get("scripts")!); // scripts itself is disabled
  assert.equal(view.focus(), "root");
});

test("deleting the focused node uses the deterministic fallback", () => {
  const view = makeView();
  const model = view.model;
  model.expand("src");
  view.setFocus("app");
  view.remove("app"); // next enabled visible at same index is util(disabled) -> lib
  assert.equal(view.focus(), "lib");
  view.remove("lib"); // nothing enabled after within src... next visible is docs
  assert.equal(view.focus(), "docs");
  view.remove("readme"); // last row removed -> nearest enabled before
  assert.equal(view.focus(), "docs");
});

test("focus cannot be set on disabled or hidden nodes", () => {
  const view = makeView();
  view.setFocus("util");
  assert.notEqual(view.focus(), "util");
  view.setFocus("app"); // hidden while src is collapsed
  assert.notEqual(view.focus(), "app");
});
