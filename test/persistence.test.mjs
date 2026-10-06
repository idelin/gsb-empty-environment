import { HistoryStore, VersionTree } from "../dist/index.js";
import { test, assert } from "./harness.mjs";

function makeBranchedStore() {
  const store = HistoryStore.create({ initialState: "a", now: () => 0 });
  store.dispatch("edit", "b"); // v2
  store.dispatch("edit", "c"); // v3 (main head)
  store.editFrom("v2", "alt", "b2", "feature"); // v4 on feature
  store.dispatch("edit", "b3"); // v5 (feature head)
  store.checkout("v2"); // view an old version
  return store;
}

test("snapshot/restore round-trips branches, heads, view and active branch", () => {
  const store = makeBranchedStore();
  const json = JSON.stringify(store.snapshot());

  const restored = HistoryStore.restore(JSON.parse(json), { now: () => 0 });

  assert.equal(restored.activeBranch, "feature");
  assert.equal(restored.viewId, "v2", "detached view survives persistence");
  assert.equal(restored.headId, "v5");
  assert.equal(restored.isViewingHead, false);
  assert.deepEqual(restored.listBranches(), [
    { name: "main", headId: "v3", isActive: false },
    { name: "feature", headId: "v5", isActive: true },
  ]);
  assert.deepEqual(
    restored.versionTree.pathFromRoot("v5").map((n) => n.state),
    ["a", "b", "b2", "b3"],
  );
});

test("a restored store keeps accepting events on the real head", () => {
  const store = makeBranchedStore();
  const restored = HistoryStore.restore(store.snapshot(), { now: () => 0 });

  const node = restored.dispatch("edit", "b4");

  assert.equal(node.parentId, "v5");
  assert.equal(restored.headId, node.id);
  assert.equal(restored.viewId, "v2", "still viewing the old version");
  assert.equal(restored.versionTree.getBranchHeadId("main"), "v3");
});

test("editing an old version after restore still branches instead of overwriting", () => {
  const store = makeBranchedStore();
  const restored = HistoryStore.restore(store.snapshot(), { now: () => 0 });
  restored.switchBranch("main");

  const { branch, node } = restored.editFrom("v1", "rewrite", "z");

  assert.equal(branch, "branch-v1");
  assert.equal(node.parentId, "v1");
  assert.equal(restored.versionTree.getBranchHeadId("main"), "v3");
});

test("restore rejects corrupted snapshots", () => {
  const store = makeBranchedStore();
  const snapshot = store.snapshot();

  assert.throws(
    () => HistoryStore.restore({ ...snapshot, activeBranch: "ghost" }),
    /active branch "ghost" does not exist/,
  );
  assert.throws(
    () => HistoryStore.restore({ ...snapshot, viewId: "ghost" }),
    /view version "ghost" does not exist/,
  );
  assert.throws(
    () =>
      HistoryStore.restore({
        ...snapshot,
        tree: { nodes: snapshot.tree.nodes, branches: { main: "ghost" } },
      }),
    /points at unknown version/,
  );
});

test("VersionTree JSON round-trip preserves structure exactly", () => {
  const store = makeBranchedStore();
  const tree = store.versionTree;

  const copy = VersionTree.fromJSON(JSON.parse(JSON.stringify(tree.toJSON())));

  assert.deepEqual(copy.toJSON(), tree.toJSON());
  assert.deepEqual(
    copy.getChildren("v2").map((n) => n.id),
    tree.getChildren("v2").map((n) => n.id),
  );
  assert.equal(copy.isBranchTip("v3"), true);
  assert.equal(copy.isBranchTip("v2"), false);
});

test("snapshots are decoupled: mutating the store does not mutate the snapshot", () => {
  const store = makeBranchedStore();
  const snapshot = store.snapshot();
  const nodesBefore = snapshot.tree.nodes.length;

  store.switchBranch("main");
  store.dispatch("edit", "d");

  assert.equal(snapshot.tree.nodes.length, nodesBefore);
  assert.equal(snapshot.activeBranch, "feature");
});
