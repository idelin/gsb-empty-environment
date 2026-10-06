import { HistoryStore } from "../dist/index.js";
import { test, assert } from "./harness.mjs";

function makeStore() {
  const store = HistoryStore.create({ initialState: 0, now: () => 0 });
  store.dispatch("add", 1);
  store.dispatch("add", 2);
  return store; // v1(init,0) <- v2(add,1) <- v3(add,2), head = v3
}

test("checkout views an old version without moving the real head", () => {
  const store = makeStore();
  const headBefore = store.headId;

  const view = store.checkout("v1");

  assert.equal(view.versionId, "v1");
  assert.equal(view.state, 0);
  assert.equal(view.isHead, false);
  assert.equal(store.viewId, "v1");
  assert.equal(store.headId, headBefore, "head must not move");
  assert.equal(store.isViewingHead, false);
  assert.equal(store.getHeadState(), 2, "head state unchanged");
});

test("new events keep landing on the head while viewing an old version", () => {
  const store = makeStore();
  store.checkout("v1");

  const node = store.dispatch("add", 3);

  assert.equal(node.id, "v4");
  assert.equal(node.parentId, "v3", "appends after the real head, not the view");
  assert.equal(store.headId, "v4");
  assert.equal(store.getHeadState(), 3);
  assert.equal(store.viewId, "v1", "view cursor stays on the old version");
  assert.equal(store.getView().state, 0);
});

test("dispatch notifies head listeners but not view listeners while viewing old", () => {
  const store = makeStore();
  const headEvents = [];
  const viewEvents = [];
  store.subscribe((node, branch) => headEvents.push([node.id, branch]));
  store.subscribeView((view) => viewEvents.push(view.versionId));

  store.checkout("v2");
  assert.deepEqual(viewEvents, ["v2"]);

  store.dispatch("add", 3);
  assert.deepEqual(headEvents, [["v4", "main"]]);
  assert.deepEqual(viewEvents, ["v2"], "view did not move, no extra notification");
});

test("backToHead returns the view to the real head", () => {
  const store = makeStore();
  store.checkout("v1");
  store.dispatch("add", 3);

  const view = store.backToHead();

  assert.equal(view.versionId, "v4");
  assert.equal(view.isHead, true);
  assert.equal(view.state, 3);
});

test("checkout rejects unknown versions", () => {
  const store = makeStore();
  assert.throws(() => store.checkout("nope"), /Unknown version "nope"/);
});

test("view follows the head when not detached", () => {
  const store = makeStore();
  assert.equal(store.viewId, "v3");

  const viewEvents = [];
  store.subscribeView((view) => viewEvents.push(view.versionId));
  store.dispatch("add", 3);

  assert.equal(store.viewId, "v4", "view at head tracks new events");
  assert.deepEqual(viewEvents, ["v4"]);
});
