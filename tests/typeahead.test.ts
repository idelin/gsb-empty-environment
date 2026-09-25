import { test } from "node:test";
import assert from "node:assert/strict";
import { TreeModel } from "../src/model.ts";
import { TreeView } from "../src/view.ts";
import { treeFixture } from "../src/fixtures.ts";

function makeView(): TreeView {
  const view = new TreeView(new TreeModel(treeFixture()));
  view.model.expand("src");
  view.model.expand("docs");
  return view;
}

test("type-ahead focuses the next matching enabled visible node", () => {
  const view = makeView();
  view.home();
  view.type("a");
  assert.equal(view.focus(), "app");
  view.clearTypeAhead();
  view.type("u"); // util.ts is disabled -> never matched
  assert.notEqual(view.focus(), "util");
});

test("repeating a character cycles through matches", () => {
  const view = makeView();
  view.home();
  view.type("a");
  assert.equal(view.focus(), "app");
  view.type("a");
  assert.equal(view.focus(), "api");
  view.type("a"); // wraps
  assert.equal(view.focus(), "app");
});

test("multi-character buffer narrows the match", () => {
  const view = makeView();
  view.home();
  view.type("g");
  assert.equal(view.focus(), "guide");
  view.clearTypeAhead();
  view.type("a");
  view.type("p");
  assert.equal(view.focus(), "app");
});

test("type-ahead state is bound to the tree version", () => {
  const view = makeView();
  view.home();
  view.type("a");
  assert.equal(view.focus(), "app");
  const boundAt = view.typeAheadVersionBound;
  view.model.insert("docs", { id: "about", label: "about.md" }, 0);
  assert.ok(view.model.version > boundAt);
  // Buffer restarts against the new tree: 'a' now matches the new node first
  // when searching after the current focus.
  view.type("a");
  assert.equal(view.focus(), "about");
});
