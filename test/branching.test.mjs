import { HistoryStore } from "../dist/index.js";
import { test, assert } from "./harness.mjs";

function makeStore() {
  const store = HistoryStore.create({ initialState: "a", now: () => 0 });
  store.dispatch("edit", "b");
  store.dispatch("edit", "c");
  return store; // main: v1(a) <- v2(b) <- v3(c)
}

test("editing an old version explicitly creates a branch", () => {
  const store = makeStore();
  const mainHeadBefore = store.headId;

  const { branch, node } = store.editFrom("v2", "alt-edit", "b2", "feature");

  assert.equal(branch, "feature");
  assert.equal(node.parentId, "v2", "edit forks from the old version");
  assert.equal(node.branch, "feature");
  assert.equal(store.activeBranch, "feature");
  assert.equal(store.headId, node.id);
  assert.equal(store.viewId, node.id, "view follows the new edit");

  const tree = store.versionTree;
  assert.equal(tree.getBranchHeadId("main"), mainHeadBefore, "main head untouched");
  assert.equal(tree.getBranchHeadId("feature"), node.id);
  assert.deepEqual(
    tree.getChildren("v2").map((child) => child.id),
    ["v3", node.id],
    "version tree records the fork",
  );
});

test("main branch can never be overwritten by editing an old version", () => {
  const store = makeStore();

  assert.throws(
    () => store.editFrom("v1", "rewrite", "x", "main"),
    /Branch "main" already exists/,
  );

  assert.equal(store.activeBranch, "main", "failed edit must not switch branches");
  assert.equal(store.headId, "v3", "main head unchanged");
  assert.equal(store.versionTree.size, 3, "no node was appended");
});

test("an existing branch name is rejected instead of overwritten", () => {
  const store = makeStore();
  store.editFrom("v2", "alt-edit", "b2", "feature");

  assert.throws(
    () => store.editFrom("v1", "other", "x", "feature"),
    /Branch "feature" already exists/,
  );
  assert.equal(store.versionTree.getBranchHead("feature").state, "b2");
});

test("branch name is auto-generated when omitted", () => {
  const store = makeStore();
  const first = store.editFrom("v1", "e1", "x");
  assert.equal(first.branch, "branch-v1");

  const second = store.editFrom("v1", "e2", "y");
  assert.equal(second.branch, "branch-v1-2", "auto names never collide");
});

test("editing at the real head is a plain dispatch, no branch created", () => {
  const store = makeStore();

  const { branch, node } = store.editFrom("v3", "edit", "d");

  assert.equal(branch, "main");
  assert.equal(node.parentId, "v3");
  assert.deepEqual(store.versionTree.listBranches(), ["main"]);
  assert.equal(store.headId, node.id);
});

test("events dispatched while viewing an old version do not fork", () => {
  const store = makeStore();
  store.checkout("v1");

  store.dispatch("edit", "d");

  assert.deepEqual(store.versionTree.listBranches(), ["main"]);
  assert.equal(store.versionTree.getNode("v4").parentId, "v3");
  assert.equal(store.viewId, "v1");
});

test("persistent tree is immutable: old snapshots keep their shape", () => {
  const store = makeStore();
  const treeBefore = store.versionTree;

  store.editFrom("v1", "alt", "x", "feature");
  store.dispatch("more", "y");

  assert.equal(treeBefore.size, 3);
  assert.deepEqual(treeBefore.listBranches(), ["main"]);
  assert.equal(treeBefore.getBranchHeadId("main"), "v3");
  assert.equal(store.versionTree.size, 5);
  assert.deepEqual(store.versionTree.listBranches(), ["main", "feature"]);
});
