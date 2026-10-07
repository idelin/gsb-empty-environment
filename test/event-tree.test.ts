import { test } from "node:test";
import assert from "node:assert/strict";
import { EventTree, TreeEvent, type NodeId } from "../src/event-tree.ts";

interface Fixture {
  tree: EventTree;
  ids: Record<string, NodeId>;
  log: string[];
  listen: (
    node: string,
    type: string,
    tag: string,
    options?: { capture?: boolean; once?: boolean },
    extra?: (e: TreeEvent<string>) => void,
  ) => void;
}

function makeTree(): Fixture {
  const tree = new EventTree();
  const log: string[] = [];
  const root = tree.createNode();
  const a = tree.createNode(root);
  const b = tree.createNode(a);
  const c = tree.createNode(b);
  const ids: Record<string, NodeId> = { root, a, b, c };
  const listen: Fixture["listen"] = (node, type, tag, options, extra) => {
    tree.addEventListener<string>(
      ids[node],
      type,
      (e) => {
        if (tag) log.push(tag);
        extra?.(e);
      },
      options,
    );
  };
  return { tree, ids, log, listen };
}

test("capture -> target -> bubble order along the virtual path", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("root", "hit", "root:capture", { capture: true });
  listen("root", "hit", "root:bubble");
  listen("a", "hit", "a:capture", { capture: true });
  listen("a", "hit", "a:bubble");
  listen("b", "hit", "b:capture", { capture: true });
  listen("b", "hit", "b:bubble");
  listen("c", "hit", "c:capture", { capture: true });
  listen("c", "hit", "c:bubble");

  const ev = tree.dispatchEvent<string>(ids.c, { type: "hit", data: "d" });

  assert.deepEqual(log, [
    "root:capture",
    "a:capture",
    "b:capture",
    "c:capture",
    "c:bubble",
    "b:bubble",
    "a:bubble",
    "root:bubble",
  ]);
  assert.equal(ev.target, ids.c);
  assert.equal(ev.phase, "none");
  assert.equal(ev.currentTarget, null);
});

test("target and currentTarget are set correctly at each step", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("a", "hit", "", { capture: true }, (e) => {
    log.push(`cap target=${e.target} current=${e.currentTarget} phase=${e.phase}`);
  });
  listen("c", "hit", "", {}, (e) => {
    log.push(`tgt target=${e.target} current=${e.currentTarget} phase=${e.phase}`);
  });
  tree.dispatchEvent<string>(ids.c, { type: "hit" });
  assert.deepEqual(log, [
    `cap target=${ids.c} current=${ids.a} phase=capture`,
    `tgt target=${ids.c} current=${ids.c} phase=target`,
  ]);
});

test("non-bubbling event skips the bubble phase", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("root", "hit", "root:capture", { capture: true });
  listen("root", "hit", "root:bubble");
  listen("c", "hit", "c:target");
  tree.dispatchEvent<string>(ids.c, { type: "hit", bubbles: false });
  assert.deepEqual(log, ["root:capture", "c:target"]);
});

test("once listener is removed before invocation (reentrant dispatch does not retrigger it)", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("b", "hit", "b:once", { once: true }, (e) => {
    if (e.data === "outer") {
      tree.dispatchEvent<string>(ids.c, { type: "hit", data: "inner" });
    }
  });
  listen("a", "hit", "", {}, (e) => log.push(`a:bubble:${e.data}`));
  listen("root", "hit", "", {}, (e) => log.push(`root:bubble:${e.data}`));

  tree.dispatchEvent<string>(ids.c, { type: "hit", data: "outer" });
  tree.dispatchEvent<string>(ids.c, { type: "hit", data: "third" });

  assert.deepEqual(log, [
    "b:once",
    "a:bubble:inner",
    "root:bubble:inner",
    "a:bubble:outer",
    "root:bubble:outer",
    "a:bubble:third",
    "root:bubble:third",
  ]);
});

test("stopImmediatePropagation blocks remaining listeners on the node and all later propagation", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("b", "hit", "b:1", {}, (e) => e.stopImmediatePropagation());
  listen("b", "hit", "b:2");
  listen("b", "hit", "b:3");
  listen("a", "hit", "a:bubble");
  listen("root", "hit", "root:bubble");

  const ev = tree.dispatchEvent<string>(ids.c, { type: "hit" });

  assert.deepEqual(log, ["b:1"]);
  assert.equal(ev.immediatePropagationStopped, true);
  assert.equal(ev.propagationStopped, true);
});

test("stopImmediatePropagation during capture prevents target and bubble phases", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("a", "hit", "a:capture", { capture: true }, (e) => e.stopImmediatePropagation());
  listen("b", "hit", "b:capture", { capture: true });
  listen("c", "hit", "c:target");
  listen("root", "hit", "root:bubble");

  tree.dispatchEvent<string>(ids.c, { type: "hit" });

  assert.deepEqual(log, ["a:capture"]);
});

test("stopPropagation finishes the current node but stops later nodes", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("b", "hit", "b:1", {}, (e) => e.stopPropagation());
  listen("b", "hit", "b:2");
  listen("a", "hit", "a:bubble");
  listen("root", "hit", "root:bubble");

  const ev = tree.dispatchEvent<string>(ids.c, { type: "hit" });

  assert.deepEqual(log, ["b:1", "b:2"]);
  assert.equal(ev.propagationStopped, true);
  assert.equal(ev.immediatePropagationStopped, false);
});

test("adding or removing nodes during dispatch does not change the current path", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("root", "hit", "root:capture", { capture: true }, () => {
    tree.removeNode(ids.b);
    const x = tree.createNode(ids.a);
    tree.setParent(ids.c, x);
  });
  listen("a", "hit", "a:capture", { capture: true });
  listen("b", "hit", "b:capture", { capture: true });
  listen("c", "hit", "c:target");
  listen("b", "hit", "b:bubble");
  listen("a", "hit", "a:bubble");
  listen("root", "hit", "root:bubble");

  tree.dispatchEvent<string>(ids.c, { type: "hit" });

  assert.deepEqual(log, [
    "root:capture",
    "a:capture",
    "b:capture",
    "c:target",
    "b:bubble",
    "a:bubble",
    "root:bubble",
  ]);
});

test("removing a listener during dispatch takes effect immediately", () => {
  const { tree, ids, log, listen } = makeTree();
  const b2 = () => log.push("b:2");
  const bCap = () => log.push("b:capture");
  const aBubble = () => log.push("a:bubble");

  tree.addEventListener(ids.root, "hit", () => {
    log.push("root:capture");
    tree.removeEventListener(ids.b, "hit", bCap, { capture: true });
    tree.removeEventListener(ids.a, "hit", aBubble);
  }, { capture: true });
  tree.addEventListener(ids.b, "hit", bCap, { capture: true });
  tree.addEventListener(ids.b, "hit", () => {
    log.push("b:1");
    tree.removeEventListener(ids.b, "hit", b2);
  });
  tree.addEventListener(ids.b, "hit", b2);
  tree.addEventListener(ids.a, "hit", aBubble);
  tree.addEventListener(ids.c, "hit", () => log.push("c:target"));

  tree.dispatchEvent<string>(ids.c, { type: "hit" });

  assert.deepEqual(log, ["root:capture", "c:target", "b:1"]);
});

test("reentrant dispatch runs the inner event to completion before the outer continues", () => {
  const tree = new EventTree();
  const log: string[] = [];
  const root = tree.createNode();
  const a = tree.createNode(root);
  const b = tree.createNode(a);
  const p = tree.createNode(root);
  const y = tree.createNode(p);

  tree.addEventListener<string>(root, "inner", () => log.push("root:cap:inner"), { capture: true });
  tree.addEventListener<string>(p, "inner", () => log.push("p:cap:inner"), { capture: true });
  tree.addEventListener<string>(y, "inner", () => log.push("y:target:inner"));
  tree.addEventListener<string>(p, "inner", () => log.push("p:bub:inner"));
  tree.addEventListener<string>(root, "inner", () => log.push("root:bub:inner"));

  tree.addEventListener<string>(b, "outer", () => {
    log.push("b:bub:outer");
    tree.dispatchEvent<string>(y, { type: "inner" });
    log.push("b:after-inner");
  });
  tree.addEventListener<string>(a, "outer", () => log.push("a:bub:outer"));
  tree.addEventListener<string>(root, "outer", () => log.push("root:bub:outer"));

  tree.dispatchEvent<string>(b, { type: "outer" });

  assert.deepEqual(log, [
    "b:bub:outer",
    "root:cap:inner",
    "p:cap:inner",
    "y:target:inner",
    "p:bub:inner",
    "root:bub:inner",
    "b:after-inner",
    "a:bub:outer",
    "root:bub:outer",
  ]);
});

test("a throwing listener does not stop dispatch; errors are aggregated and rethrown", () => {
  const { tree, ids, log, listen } = makeTree();
  listen("b", "hit", "b:1", {}, () => {
    throw new Error("boom");
  });
  listen("b", "hit", "b:2");
  listen("a", "hit", "a:bubble");
  listen("root", "hit", "root:bubble");
  listen("c", "hit", "c:target");

  let caught: unknown = null;
  try {
    tree.dispatchEvent<string>(ids.c, { type: "hit" });
  } catch (err) {
    caught = err;
  }

  assert.deepEqual(log, ["c:target", "b:1", "b:2", "a:bubble", "root:bubble"]);
  assert.ok(caught instanceof AggregateError);
  assert.equal((caught as AggregateError).errors.length, 1);
  assert.equal(((caught as AggregateError).errors[0] as Error).message, "boom");
});

test("onListenerError option reports errors without throwing", () => {
  const seen: Array<{ node: NodeId; phase: string; message: string }> = [];
  const tree = new EventTree({
    onListenerError: (info) =>
      seen.push({ node: info.node, phase: info.phase, message: (info.error as Error).message }),
  });
  const root = tree.createNode();
  const a = tree.createNode(root);
  const log: string[] = [];
  tree.addEventListener(a, "hit", () => {
    throw new Error("kaboom");
  });
  tree.addEventListener(root, "hit", () => log.push("root:bubble"));

  const ev = tree.dispatchEvent<string>(a, { type: "hit" });

  assert.deepEqual(log, ["root:bubble"]);
  assert.deepEqual(seen, [{ node: a, phase: "target", message: "kaboom" }]);
  assert.equal(ev.phase, "none");
});

test("duplicate registration of the same listener and capture flag is ignored", () => {
  const { tree, ids, log } = makeTree();
  const fn = () => log.push("hit");
  tree.addEventListener(ids.c, "hit", fn);
  tree.addEventListener(ids.c, "hit", fn);
  tree.addEventListener(ids.c, "hit", fn, { capture: true });
  tree.dispatchEvent<string>(ids.c, { type: "hit" });
  assert.deepEqual(log, ["hit", "hit"]);
});
