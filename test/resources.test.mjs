import { HistoryStore } from "../dist/index.js";
import { test, assert } from "./harness.mjs";

function makeStore() {
  const store = HistoryStore.create({ initialState: 0, now: () => 0 });
  store.dispatch("add", 1);
  store.dispatch("add", 2);
  return store; // main: v1 <- v2 <- v3
}

function recorder(log) {
  return (view) => {
    log.push(`setup:${view.versionId}`);
    return () => log.push(`cleanup:${view.versionId}`);
  };
}

test("watchView sets up immediately and cleans up on checkout", () => {
  const store = makeStore();
  const log = [];
  store.watchView(recorder(log));
  assert.deepEqual(log, ["setup:v3"]);

  store.checkout("v1");
  assert.deepEqual(log, ["setup:v3", "cleanup:v3", "setup:v1"]);

  store.backToHead();
  assert.deepEqual(log, ["setup:v3", "cleanup:v3", "setup:v1", "cleanup:v1", "setup:v3"]);
});

test("dispatch while viewing an old version does not churn resources", () => {
  const store = makeStore();
  store.checkout("v1");
  const log = [];
  store.watchView(recorder(log));
  assert.deepEqual(log, ["setup:v1"]);

  store.dispatch("add", 3);

  assert.deepEqual(log, ["setup:v1"], "view unchanged: no cleanup, no re-setup");
});

test("branch switch cleans up old view resources and sets up new ones", () => {
  const store = makeStore();
  store.editFrom("v1", "alt", 10, "feature"); // v4 on feature
  const log = [];
  store.watchView(recorder(log));
  assert.deepEqual(log, ["setup:v4"]);

  store.switchBranch("main");
  assert.deepEqual(log, ["setup:v4", "cleanup:v4", "setup:v3"]);
});

test("branch-creating edit cleans up the previous view's resources", () => {
  const store = makeStore();
  const log = [];
  store.watchView(recorder(log));

  store.editFrom("v1", "alt", 10, "feature");

  assert.deepEqual(log, ["setup:v3", "cleanup:v3", "setup:v4"]);
});

test("releasing a watcher runs its last cleanup and stops future churn", () => {
  const store = makeStore();
  const log = [];
  const release = store.watchView(recorder(log));

  release();
  assert.deepEqual(log, ["setup:v3", "cleanup:v3"]);

  store.checkout("v1");
  store.switchBranch("main");
  assert.deepEqual(log, ["setup:v3", "cleanup:v3"], "no further calls after release");
});

test("multiple watchers each get cleanup before any re-setup", () => {
  const store = makeStore();
  const logA = [];
  const logB = [];
  store.watchView(recorder(logA));
  store.watchView(recorder(logB));

  store.checkout("v2");

  assert.deepEqual(logA, ["setup:v3", "cleanup:v3", "setup:v2"]);
  assert.deepEqual(logB, ["setup:v3", "cleanup:v3", "setup:v2"]);
});

test("resources without a cleanup function are supported", () => {
  const store = makeStore();
  const seen = [];
  store.watchView((view) => {
    seen.push(view.versionId);
  });

  store.checkout("v1");
  store.backToHead();

  assert.deepEqual(seen, ["v3", "v1", "v3"]);
});
