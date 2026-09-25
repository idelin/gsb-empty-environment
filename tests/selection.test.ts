import { test } from "node:test";
import assert from "node:assert/strict";
import { TreeModel } from "../src/model.ts";
import { TreeView } from "../src/view.ts";
import { treeFixture } from "../src/fixtures.ts";

function makeView(): TreeView {
  const view = new TreeView(new TreeModel(treeFixture()));
  view.model.expand("src");
  view.model.expand("docs");
  view.model.expand("scripts");
  return view;
}

test("selection and focus are independent", () => {
  const view = makeView();
  view.select("app");
  assert.deepEqual(view.selection(), ["app"]);
  assert.equal(view.focus(), "app");
  view.moveDown();
  view.moveDown();
  assert.equal(view.focus(), "docs");
  assert.deepEqual(view.selection(), ["app"]); // selection untouched by focus moves
});

test("ctrl-toggle builds a multi-selection; disabled nodes are ignored", () => {
  const view = makeView();
  view.select("app");
  view.toggleSelect("guide");
  view.toggleSelect("util"); // disabled -> ignored
  view.toggleSelect("scripts"); // disabled -> ignored
  assert.deepEqual(view.selection().sort(), ["app", "guide"]);
  view.toggleSelect("guide"); // toggle off
  assert.deepEqual(view.selection(), ["app"]);
});

test("shift range follows visible order and skips disabled rows", () => {
  const view = makeView();
  view.select("app");
  view.selectRange("build");
  // visible order: root src app util lib docs guide api scripts build clean readme
  assert.deepEqual(view.selection(), ["app", "lib", "docs", "guide", "api", "build"]);
});

test("shift range works upwards from the anchor", () => {
  const view = makeView();
  view.select("api");
  view.selectRange("src");
  assert.deepEqual(view.selection(), ["src", "app", "lib", "docs", "guide", "api"]);
});

test("anchor falls back to focus when the anchor node is removed", () => {
  const view = makeView();
  view.select("app");
  view.toggleSelect("guide"); // anchor = guide
  view.remove("guide");
  assert.equal(view.anchor(), view.focus());
  // focus fell back to "api" when guide was removed; anchor follows focus
  view.selectRange("readme");
  assert.deepEqual(view.selection(), ["api", "build", "clean", "readme"]);
});

test("nodes that become disabled drop out of the selection", () => {
  const view = makeView();
  view.select("app");
  view.toggleSelect("guide");
  view.model.setDisabled("guide", true);
  assert.deepEqual(view.selection(), ["app"]);
});

test("hidden-by-filter nodes keep their selection", () => {
  const view = makeView();
  view.select("app");
  view.model.setFilter((label) => label.includes("md"));
  assert.deepEqual(view.selection(), ["app"]);
  view.model.setFilter(null);
  assert.deepEqual(view.selection(), ["app"]);
});
