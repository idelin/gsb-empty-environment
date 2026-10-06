import { HistoryStore } from "../dist/index.js";
import { test, assert } from "./harness.mjs";

function makeBranchedStore() {
  const store = HistoryStore.create({ initialState: "a", now: () => 0 });
  store.dispatch("edit", "b"); // v2
  store.dispatch("edit", "c"); // v3 (main head)
  store.editFrom("v2", "alt", "b2", "feature"); // v4 on feature
  store.dispatch("edit", "b3"); // v5 (feature head)
  return store;
}

test("switchBranch moves the active branch and resets the view to its head", () => {
  const store = makeBranchedStore();
  assert.equal(store.activeBranch, "feature");
  assert.equal(store.headId, "v5");

  const view = store.switchBranch("main");

  assert.equal(store.activeBranch, "main");
  assert.equal(view.versionId, "v3");
  assert.equal(view.isHead, true);
  assert.equal(view.state, "c");
});

test("after switching, new events land on the newly active branch only", () => {
  const store = makeBranchedStore();
  store.switchBranch("main");

  const node = store.dispatch("edit", "d");

  const tree = store.versionTree;
  assert.equal(node.branch, "main");
  assert.equal(node.parentId, "v3");
  assert.equal(tree.getBranchHeadId("main"), node.id);
  assert.equal(tree.getBranchHeadId("feature"), "v5", "feature head untouched");
});

test("switching back to the same branch resets a detached view to its head", () => {
  const store = makeBranchedStore();
  store.checkout("v2");
  assert.equal(store.isViewingHead, false);

  store.switchBranch("feature");

  assert.equal(store.viewId, "v5");
  assert.equal(store.isViewingHead, true);
});

test("switching to an unknown branch throws and changes nothing", () => {
  const store = makeBranchedStore();

  assert.throws(() => store.switchBranch("nope"), /Unknown branch "nope"/);
  assert.equal(store.activeBranch, "feature");
  assert.equal(store.headId, "v5");
});

test("listBranches reports heads and the active branch", () => {
  const store = makeBranchedStore();
  store.switchBranch("main");

  assert.deepEqual(store.listBranches(), [
    { name: "main", headId: "v3", isActive: true },
    { name: "feature", headId: "v5", isActive: false },
  ]);
});

test("both branches keep full independent histories in the version tree", () => {
  const store = makeBranchedStore();
  const tree = store.versionTree;

  assert.deepEqual(
    tree.pathFromRoot(tree.getBranchHeadId("main")).map((n) => n.state),
    ["a", "b", "c"],
  );
  assert.deepEqual(
    tree.pathFromRoot(tree.getBranchHeadId("feature")).map((n) => n.state),
    ["a", "b", "b2", "b3"],
  );
});
