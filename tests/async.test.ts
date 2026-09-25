import { test } from "node:test";
import assert from "node:assert/strict";
import { TreeModel } from "../src/model.ts";
import { TreeView } from "../src/view.ts";
import { DeferredLoader, flush, lazyChildrenFixture, treeFixture } from "../src/fixtures.ts";

function make() {
  const loader = new DeferredLoader(lazyChildrenFixture());
  const model = new TreeModel(treeFixture(), loader.load);
  const view = new TreeView(model);
  return { loader, model, view };
}

test("expanding a lazy node loads children asynchronously", async () => {
  const { loader, model } = make();
  model.expand("src");
  model.expand("lib");
  assert.equal(model.get("lib")!.loading, true);
  assert.deepEqual(loader.calls, ["lib"]);
  loader.resolveNext("lib");
  await flush();
  assert.deepEqual(model.visibleIds(), [
    "root", "src", "app", "util", "lib", "lib-core", "lib-io", "lib-extra",
    "docs", "scripts", "readme",
  ]);
});

test("a stale load never re-expands a branch the user closed", async () => {
  const { loader, model } = make();
  model.expand("src");
  model.expand("lib");
  model.collapse("lib"); // user closes the branch while the load is in flight
  loader.resolveNext("lib");
  await flush();
  const lib = model.get("lib")!;
  assert.equal(lib.expanded, false); // still collapsed
  assert.equal(lib.loaded, true); // children are there, just hidden
  assert.deepEqual(model.visibleIds(), ["root", "src", "app", "util", "lib", "docs", "scripts", "readme"]);
  model.expand("lib"); // children appear instantly from the applied load
  assert.deepEqual(model.visibleIds(), [
    "root", "src", "app", "util", "lib", "lib-core", "lib-io", "lib-extra",
    "docs", "scripts", "readme",
  ]);
});

test("a superseded load result is dropped", async () => {
  const { loader, model } = make();
  model.expand("src");
  model.expand("lib");
  model.reload("lib"); // supersedes the first in-flight load
  assert.equal(loader.pendingCount("lib"), 2);
  loader.resolveNext("lib"); // resolves the FIRST (stale) load
  await flush();
  assert.equal(model.get("lib")!.loaded, false); // stale result dropped
  loader.resolveNext("lib"); // resolves the current load
  await flush();
  assert.equal(model.get("lib")!.loaded, true);
  assert.deepEqual(
    model.get("lib")!.children.map((c) => c.id),
    ["lib-core", "lib-io", "lib-extra"], // applied exactly once
  );
});

test("cancelling a load drops its eventual result", async () => {
  const { loader, model } = make();
  model.expand("src");
  model.expand("lib");
  model.cancelLoad("lib");
  loader.resolveNext("lib");
  await flush();
  assert.equal(model.get("lib")!.loaded, false);
  assert.equal(model.get("lib")!.children.length, 0);
});

test("removing a node with an in-flight load drops the result", async () => {
  const { loader, model } = make();
  model.expand("src");
  model.expand("lib");
  model.remove("lib");
  loader.resolveNext("lib");
  await flush();
  assert.equal(model.has("lib-core"), false);
});

test("async-added children do not reset an existing selection", async () => {
  const { loader, model, view } = make();
  model.expand("src");
  view.select("app");
  view.toggleSelect("docs");
  model.expand("lib");
  loader.resolveNext("lib");
  await flush();
  assert.deepEqual(view.selection().sort(), ["app", "docs"]);
  assert.equal(view.focus(), "docs");
});

test("reordering preserves selection and focus, updates visible order", () => {
  const { model, view } = make();
  model.expand("src");
  view.select("app");
  view.toggleSelect("readme");
  model.move("docs", "src", 0); // move docs into src, first position
  assert.deepEqual(model.visibleIds(), [
    "root", "src", "docs", "app", "util", "lib", "scripts", "readme",
  ]);
  assert.deepEqual(view.selection().sort(), ["app", "readme"]);
  assert.equal(view.focus(), "readme");
});

test("filter keeps ancestors of matches and revalidates focus", () => {
  const { model, view } = make();
  model.expand("src");
  model.expand("docs");
  view.setFocus("app");
  model.setFilter((label) => label.includes("api"));
  assert.deepEqual(model.visibleIds(), ["root", "docs", "api"]);
  // focus was on app (now hidden) -> deterministic fallback
  assert.equal(view.focus(), "root");
  model.setFilter(null);
  assert.deepEqual(model.visibleIds(), [
    "root", "src", "app", "util", "lib", "docs", "guide", "api", "scripts", "readme",
  ]);
});
