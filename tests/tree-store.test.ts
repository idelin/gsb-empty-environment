import assert from "node:assert/strict";
import { test } from "node:test";
import { TreeStore } from "../src/index.ts";
import type { ChildData, NodeId } from "../src/index.ts";

/** A promise whose settlement is controlled by the test. */
class Deferred<T> {
  readonly promise: Promise<T>;
  private resolveFn!: (value: T) => void;
  private rejectFn!: (reason: unknown) => void;

  constructor() {
    this.promise = new Promise<T>((resolve, reject) => {
      this.resolveFn = resolve;
      this.rejectFn = reject;
    });
  }

  resolve(value: T): void {
    this.resolveFn(value);
  }

  reject(reason: unknown): void {
    this.rejectFn(reason);
  }
}

/**
 * Loader mock: records every invocation and hands out deferred promises so
 * the test can settle responses in any order it wants.
 */
class MockLoader {
  readonly calls: NodeId[] = [];
  private readonly queues = new Map<NodeId, Deferred<readonly ChildData[]>[]>();

  readonly load = (id: NodeId): Promise<readonly ChildData[]> => {
    this.calls.push(id);
    const deferred = new Deferred<readonly ChildData[]>();
    const queue = this.queues.get(id) ?? [];
    queue.push(deferred);
    this.queues.set(id, queue);
    return deferred.promise;
  };

  get pendingCount(): number {
    let total = 0;
    for (const queue of this.queues.values()) total += queue.length;
    return total;
  }

  /** Resolve the oldest unsettled request for `id`. */
  resolveNext(id: NodeId, children: readonly ChildData[]): void {
    this.resolveAt(id, 0, children);
  }

  rejectNext(id: NodeId, reason: unknown): void {
    this.rejectAt(id, 0, reason);
  }

  /** Resolve the n-th unsettled request for `id`, out of order if needed. */
  resolveAt(id: NodeId, index: number, children: readonly ChildData[]): void {
    this.take(id, index).resolve(children);
  }

  rejectAt(id: NodeId, index: number, reason: unknown): void {
    this.take(id, index).reject(reason);
  }

  private take(id: NodeId, index: number): Deferred<readonly ChildData[]> {
    const queue = this.queues.get(id);
    assert.ok(queue && queue.length > index, `no pending request #${index} for ${id}`);
    return queue.splice(index, 1)[0] as Deferred<readonly ChildData[]>;
  }
}

const kids = (...ids: NodeId[]): ChildData[] => ids.map((id) => ({ id }));

/** Let resolved/rejected promises run their continuations. */
async function flush(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

function makeStore(): { store: TreeStore; loader: MockLoader } {
  const loader = new MockLoader();
  return { store: new TreeStore({ loader: loader.load }), loader };
}

test("expand loads children and selection path walks up to the root", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  assert.equal(store.getNode("root")?.status, "loading");
  loader.resolveNext("root", kids("a", "b"));
  await flush();

  store.expand("a");
  loader.resolveNext("a", kids("c"));
  await flush();

  assert.deepEqual(store.getChildIds("root"), ["a", "b"]);
  assert.deepEqual(store.getChildIds("a"), ["c"]);
  assert.equal(store.getNode("a")?.status, "loaded");

  store.select("c");
  assert.deepEqual(store.getSelectionPath(), ["root", "a", "c"]);
  assert.equal(loader.calls.length, 2);
});

test("same generation shares a single in-flight request", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  store.expand("root");
  store.expand("root");
  assert.equal(loader.calls.length, 1, "loader must be invoked exactly once");
  assert.equal(store.isLoading("root"), true);

  loader.resolveNext("root", kids("a"));
  await flush();

  store.expand("root"); // already loaded: no reload
  assert.equal(loader.calls.length, 1);
  assert.deepEqual(store.getChildIds("root"), ["a"]);
});

test("collapse then re-expand: the stale response is discarded (old settles first)", async () => {
  const { store, loader } = makeStore();

  store.expand("root"); // request #1, generation 0
  store.collapse("root"); // bumps generation, request #1 becomes stale
  store.expand("root"); // request #2, generation 1
  assert.equal(loader.calls.length, 2);

  loader.resolveNext("root", kids("stale")); // settles request #1
  await flush();
  assert.equal(store.getNode("root")?.status, "loading");
  assert.deepEqual(store.getChildIds("root"), []);

  loader.resolveNext("root", kids("fresh")); // settles request #2
  await flush();
  assert.equal(store.getNode("root")?.status, "loaded");
  assert.deepEqual(store.getChildIds("root"), ["fresh"]);
  assert.equal(store.has("stale"), false);
  assert.equal(loader.calls.length, 2, "no extra request may sneak in");
});

test("collapse then re-expand: the stale response is discarded (new settles first)", async () => {
  const { store, loader } = makeStore();

  store.expand("root"); // request #1
  store.collapse("root");
  store.expand("root"); // request #2

  loader.resolveAt("root", 1, kids("fresh")); // #2 settles first
  await flush();
  assert.deepEqual(store.getChildIds("root"), ["fresh"]);

  loader.resolveAt("root", 0, kids("stale")); // #1 arrives late
  await flush();
  assert.deepEqual(store.getChildIds("root"), ["fresh"]);
  assert.equal(store.has("stale"), false);
});

test("out-of-order settle across requests: newest generation always wins", async () => {
  const { store, loader } = makeStore();

  store.expand("root"); // #1 gen0
  store.collapse("root");
  store.expand("root"); // #2 gen1
  store.collapse("root");
  store.expand("root"); // #3 gen2
  assert.equal(loader.calls.length, 3);

  loader.resolveAt("root", 2, kids("gen2")); // #3 settles first
  loader.resolveAt("root", 1, kids("gen1")); // #2
  loader.resolveAt("root", 0, kids("gen0")); // #1 settles last
  await flush();

  assert.deepEqual(store.getChildIds("root"), ["gen2"]);
  assert.equal(store.has("gen0"), false);
  assert.equal(store.has("gen1"), false);
  assert.equal(store.getNode("root")?.status, "loaded");
});

test("failed load keeps the displayed tree and retry recovers", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  loader.resolveNext("root", kids("a", "b"));
  await flush();

  store.refresh("root"); // reload while children are displayed
  assert.equal(store.getNode("root")?.status, "loading");
  loader.rejectNext("root", new Error("network down"));
  await flush();

  assert.equal(store.getNode("root")?.status, "error");
  assert.deepEqual(
    store.getChildIds("root"),
    ["a", "b"],
    "displayed children survive a failed refresh",
  );

  store.expand("root"); // error state: expand alone must not hammer the loader
  assert.equal(loader.calls.filter((id) => id === "root").length, 2);

  store.retry("root");
  assert.equal(loader.calls.filter((id) => id === "root").length, 3);
  loader.resolveNext("root", kids("a", "b", "c"));
  await flush();

  assert.equal(store.getNode("root")?.status, "loaded");
  assert.deepEqual(store.getChildIds("root"), ["a", "b", "c"]);
});

test("stale failure after collapse is ignored and does not corrupt state", async () => {
  const { store, loader } = makeStore();

  store.expand("root"); // #1
  store.collapse("root");
  store.expand("root"); // #2
  loader.rejectNext("root", new Error("stale failure")); // rejects #1
  await flush();

  assert.equal(store.getNode("root")?.status, "loading", "still waiting on #2");
  loader.resolveNext("root", kids("a"));
  await flush();
  assert.equal(store.getNode("root")?.status, "loaded");
  assert.deepEqual(store.getChildIds("root"), ["a"]);
});

test("removing a node with a pending load leaves no ghost nodes", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  loader.resolveNext("root", kids("a"));
  await flush();

  store.expand("a"); // request for "a" now in flight
  store.remove("a"); // deleted before the response arrives
  assert.equal(store.has("a"), false);
  assert.deepEqual(store.getChildIds("root"), []);

  loader.resolveNext("a", kids("a1", "a2")); // late response for the dead node
  await flush();

  assert.equal(store.has("a"), false, "deleted node must not resurrect");
  assert.equal(store.has("a1"), false, "children of a deleted node must not appear");
  assert.equal(store.has("a2"), false);
  assert.deepEqual(store.getChildIds("root"), []);
});

test("removing a subtree drops every descendant and their pending loads", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  loader.resolveNext("root", kids("a"));
  await flush();
  store.expand("a");
  loader.resolveNext("a", kids("b"));
  await flush();
  store.expand("b"); // pending when the subtree is removed

  store.remove("a");
  loader.resolveNext("b", kids("b1")); // late response from inside the subtree
  await flush();

  for (const id of ["a", "b", "b1"]) {
    assert.equal(store.has(id), false, `${id} must be gone for good`);
  }
  assert.deepEqual(store.getChildIds("root"), []);
});

test("selection falls back to the parent when the selected node is removed", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  loader.resolveNext("root", kids("a"));
  await flush();
  store.expand("a");
  loader.resolveNext("a", kids("b"));
  await flush();
  store.expand("b");
  loader.resolveNext("b", kids("c"));
  await flush();

  store.select("c");
  assert.deepEqual(store.getSelectionPath(), ["root", "a", "b", "c"]);

  store.remove("b"); // removes b and c
  assert.equal(store.selected, "a");
  assert.deepEqual(store.getSelectionPath(), ["root", "a"]);

  store.select("a");
  store.remove("a");
  assert.equal(store.selected, "root");
  assert.deepEqual(store.getSelectionPath(), ["root"]);
});

test("refresh response preserves the state of children that still exist", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  loader.resolveNext("root", kids("a"));
  await flush();
  store.expand("a");
  loader.resolveNext("a", kids("a1"));
  await flush();
  store.select("a1");

  store.refresh("root");
  loader.resolveNext("root", kids("a", "b")); // "a" still present server-side
  await flush();

  assert.deepEqual(store.getChildIds("root"), ["a", "b"]);
  assert.deepEqual(
    store.getChildIds("a"),
    ["a1"],
    "subtree of a surviving child is kept",
  );
  assert.equal(store.getNode("a")?.expanded, true);
  assert.deepEqual(store.getSelectionPath(), ["root", "a", "a1"]);
});

test("collapse during refresh keeps old children and drops the response", async () => {
  const { store, loader } = makeStore();

  store.expand("root");
  loader.resolveNext("root", kids("a"));
  await flush();

  store.refresh("root");
  store.collapse("root"); // invalidate the refresh while it is in flight
  assert.equal(store.getNode("root")?.status, "loaded");
  assert.deepEqual(store.getChildIds("root"), ["a"]);

  loader.resolveNext("root", kids("x", "y")); // stale refresh response
  await flush();
  assert.deepEqual(store.getChildIds("root"), ["a"]);
  assert.equal(store.has("x"), false);
});

test("load count reflects dedupe, retries and invalidated requests", async () => {
  const { store, loader } = makeStore();

  store.expand("root"); // 1
  store.expand("root"); // shared
  store.collapse("root");
  store.expand("root"); // 2 (new generation)
  loader.rejectNext("root", new Error("boom")); // stale #1 fails
  loader.rejectNext("root", new Error("boom")); // #2 fails
  await flush();
  store.retry("root"); // 3
  loader.resolveNext("root", kids("a"));
  await flush();
  store.expand("root"); // loaded: no request

  assert.equal(loader.calls.length, 3);
  assert.deepEqual(loader.calls, ["root", "root", "root"]);
  assert.equal(store.loads, 3);
});
