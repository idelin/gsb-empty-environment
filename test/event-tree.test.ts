import assert from "node:assert/strict";
import {
  EventNode,
  TreeEvent,
  PHASE_CAPTURING,
  PHASE_AT_TARGET,
  PHASE_BUBBLING,
  PHASE_NONE,
} from "../src/event-tree";

let passed = 0;
let failed = 0;
const failures: string[] = [];

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    failures.push(name);
    console.error(`FAIL ${name}`);
    console.error(err);
  }
}

/** Build root -> a -> b -> c and return the nodes. */
function makeTree() {
  const root = new EventNode("root");
  const a = new EventNode("a");
  const b = new EventNode("b");
  const c = new EventNode("c");
  root.appendChild(a);
  a.appendChild(b);
  b.appendChild(c);
  return { root, a, b, c };
}

/** Listener that logs `tag` and records the event phase it observed. */
function logger(log: string[], tag: string, phases?: number[]) {
  return (e: TreeEvent) => {
    log.push(tag);
    if (phases) phases.push(e.eventPhase);
  };
}

test("capture -> target -> bubble order along the virtual tree", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  for (const [node, name] of [[root, "root"], [a, "a"], [b, "b"], [c, "c"]] as const) {
    node.addEventListener("hit", logger(log, `capture:${name}`), { capture: true });
    node.addEventListener("hit", logger(log, `bubble:${name}`));
  }
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, [
    "capture:root",
    "capture:a",
    "capture:b",
    "capture:c", // at target, capture listener registered first
    "bubble:c", // at target, bubble listener registered second
    "bubble:b",
    "bubble:a",
    "bubble:root",
  ]);
});

test("eventPhase and currentTarget are set correctly per step", () => {
  const { root, a, b, c } = makeTree();
  const seen: Array<[string, number, string]> = [];
  for (const [node, name] of [[root, "root"], [a, "a"], [b, "b"], [c, "c"]] as const) {
    node.addEventListener(
      "hit",
      (e) => seen.push([name, e.eventPhase, e.currentTarget!.name!]),
      { capture: true },
    );
    node.addEventListener("hit", (e) =>
      seen.push([name, e.eventPhase, e.currentTarget!.name!]),
    );
  }
  const event = new TreeEvent("hit");
  c.dispatchEvent(event);
  assert.deepEqual(seen, [
    ["root", PHASE_CAPTURING, "root"],
    ["a", PHASE_CAPTURING, "a"],
    ["b", PHASE_CAPTURING, "b"],
    ["c", PHASE_AT_TARGET, "c"],
    ["c", PHASE_AT_TARGET, "c"],
    ["b", PHASE_BUBBLING, "b"],
    ["a", PHASE_BUBBLING, "a"],
    ["root", PHASE_BUBBLING, "root"],
  ]);
  assert.equal(event.target, c);
  assert.equal(event.currentTarget, null);
  assert.equal(event.eventPhase, PHASE_NONE);
});

test("non-bubbling event runs capture and target only", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  for (const [node, name] of [[root, "root"], [a, "a"], [b, "b"], [c, "c"]] as const) {
    node.addEventListener("hit", logger(log, `capture:${name}`), true);
    node.addEventListener("hit", logger(log, `bubble:${name}`));
  }
  c.dispatchEvent(new TreeEvent("hit", { bubbles: false }));
  assert.deepEqual(log, [
    "capture:root",
    "capture:a",
    "capture:b",
    "capture:c",
    "bubble:c", // target listeners of both kinds still run
  ]);
});

test("once listener is removed before invocation (re-entrant safe)", () => {
  const { c } = makeTree();
  const log: string[] = [];
  c.addEventListener(
    "hit",
    () => {
      log.push("once");
      // Re-entrant dispatch from inside the once listener: the listener has
      // already been removed, so it must not run again.
      c.dispatchEvent(new TreeEvent("hit"));
      log.push("after-reentrant");
    },
    { once: true },
  );
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["once", "after-reentrant"]);
  // And a later fresh dispatch does not call it either.
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["once", "after-reentrant"]);
});

test("stopPropagation lets current node's listeners finish, skips later nodes", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  root.addEventListener("hit", logger(log, "capture:root"), true);
  a.addEventListener("hit", (e) => {
    log.push("capture:a:1");
    e.stopPropagation();
  }, true);
  a.addEventListener("hit", logger(log, "capture:a:2"), true);
  b.addEventListener("hit", logger(log, "capture:b"), true);
  c.addEventListener("hit", logger(log, "target:c"));
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["capture:root", "capture:a:1", "capture:a:2"]);
});

test("stopImmediatePropagation stops current node and all later propagation", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  root.addEventListener("hit", logger(log, "capture:root"), true);
  a.addEventListener("hit", (e) => {
    log.push("capture:a:1");
    e.stopImmediatePropagation();
  }, true);
  a.addEventListener("hit", logger(log, "capture:a:2"), true);
  b.addEventListener("hit", logger(log, "capture:b"), true);
  c.addEventListener("hit", logger(log, "target:c"));
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["capture:root", "capture:a:1"]);
});

test("stopImmediatePropagation at target blocks bubble phase", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  c.addEventListener("hit", (e) => {
    log.push("target:c:1");
    e.stopImmediatePropagation();
  });
  c.addEventListener("hit", logger(log, "target:c:2"));
  b.addEventListener("hit", logger(log, "bubble:b"));
  a.addEventListener("hit", logger(log, "bubble:a"));
  root.addEventListener("hit", logger(log, "bubble:root"));
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["target:c:1"]);
});

test("tree mutations during dispatch do not change the current path", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  const extra = new EventNode("extra");
  extra.addEventListener("hit", logger(log, "bubble:extra"));

  root.addEventListener("hit", () => {
    log.push("capture:root");
    // Detach b mid-dispatch and attach a new node between a and c.
    a.removeChild(b);
    a.appendChild(extra);
    extra.appendChild(c);
  }, true);
  a.addEventListener("hit", logger(log, "capture:a"), true);
  b.addEventListener("hit", logger(log, "capture:b"), true);
  b.addEventListener("hit", logger(log, "bubble:b"));
  a.addEventListener("hit", logger(log, "bubble:a"));
  root.addEventListener("hit", logger(log, "bubble:root"));
  c.addEventListener("hit", logger(log, "target:c"));

  c.dispatchEvent(new TreeEvent("hit"));
  // Path was snapshotted as root -> a -> b -> c: b still receives both
  // phases even though it was detached, and `extra` is not visited.
  assert.deepEqual(log, [
    "capture:root",
    "capture:a",
    "capture:b",
    "target:c",
    "bubble:b",
    "bubble:a",
    "bubble:root",
  ]);
});

test("removing a listener takes effect immediately, even mid-node", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];

  const l2 = logger(log, "target:c:2");
  c.addEventListener("hit", () => {
    log.push("target:c:1");
    c.removeEventListener("hit", l2);
  });
  c.addEventListener("hit", l2);
  c.addEventListener("hit", logger(log, "target:c:3"));

  // Removal of a listener on a node not yet reached.
  const bubbleA = logger(log, "bubble:a");
  a.addEventListener("hit", bubbleA);
  root.addEventListener("hit", () => {
    log.push("capture:root");
    a.removeEventListener("hit", bubbleA);
  }, true);

  b.addEventListener("hit", logger(log, "bubble:b"));
  root.addEventListener("hit", logger(log, "bubble:root"));

  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, [
    "capture:root",
    "target:c:1",
    "target:c:3",
    "bubble:b",
    "bubble:root",
  ]);
});

test("listener added during dispatch: skipped on current node, runs on later nodes", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];

  let added = false;
  c.addEventListener("hit", () => {
    log.push("target:c:1");
    if (added) return;
    added = true;
    // Added to the node currently being invoked: not called this dispatch.
    c.addEventListener("hit", logger(log, "target:c:late"));
    // Added to a node not yet invoked: called this dispatch.
    b.addEventListener("hit", logger(log, "bubble:b:late"));
  });
  b.addEventListener("hit", logger(log, "bubble:b"));

  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["target:c:1", "bubble:b", "bubble:b:late"]);

  // Second dispatch: the late-added target listener now runs.
  log.length = 0;
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["target:c:1", "target:c:late", "bubble:b", "bubble:b:late"]);
});

test("re-entrant dispatch completes before the outer dispatch continues", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];

  root.addEventListener("hit", logger(log, "capture:root"), true);
  a.addEventListener("hit", () => {
    log.push("capture:a");
    // Re-entrant dispatch of a different event on c.
    const inner = new TreeEvent("inner");
    c.addEventListener("inner", logger(log, "inner:target:c"), { once: true });
    b.addEventListener("inner", logger(log, "inner:capture:b"), { capture: true, once: true });
    c.dispatchEvent(inner);
    log.push("capture:a:after-inner");
  }, true);
  b.addEventListener("hit", logger(log, "capture:b"), true);
  c.addEventListener("hit", logger(log, "target:c"));
  b.addEventListener("hit", logger(log, "bubble:b"));

  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, [
    "capture:root",
    "capture:a",
    "inner:capture:b",
    "inner:target:c",
    "capture:a:after-inner",
    "capture:b",
    "target:c",
    "bubble:b",
  ]);
});

test("redispatching an in-flight event throws", () => {
  const { c } = makeTree();
  const event = new TreeEvent("hit");
  let thrown: unknown = null;
  c.addEventListener("hit", () => {
    try {
      c.dispatchEvent(event);
    } catch (err) {
      thrown = err;
    }
  });
  c.dispatchEvent(event);
  assert.ok(thrown instanceof Error);
  assert.match((thrown as Error).message, /already being dispatched/);
});

test("listener errors are collected and dispatch continues", () => {
  const { root, a, b, c } = makeTree();
  const log: string[] = [];
  const err1 = new Error("boom-1");
  const err2 = new Error("boom-2");

  root.addEventListener("hit", () => {
    log.push("capture:root");
    throw err1;
  }, true);
  a.addEventListener("hit", logger(log, "capture:a"), true);
  b.addEventListener("hit", logger(log, "capture:b"), true);
  c.addEventListener("hit", () => {
    log.push("target:c:1");
    throw err2;
  });
  c.addEventListener("hit", logger(log, "target:c:2"));
  b.addEventListener("hit", logger(log, "bubble:b"));
  a.addEventListener("hit", logger(log, "bubble:a"));
  root.addEventListener("hit", logger(log, "bubble:root"));

  const result = c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, [
    "capture:root",
    "capture:a",
    "capture:b",
    "target:c:1",
    "target:c:2",
    "bubble:b",
    "bubble:a",
    "bubble:root",
  ]);
  assert.deepEqual(result.errors, [err1, err2]);
  assert.equal(result.canceled, false);
});

test("errors thrown inside a re-entrant dispatch stay in the inner result", () => {
  const { c } = makeTree();
  const innerErr = new Error("inner-boom");
  let innerErrors: unknown[] = [];
  c.addEventListener("outer", () => {
    c.addEventListener("inner", () => {
      throw innerErr;
    }, { once: true });
    innerErrors = c.dispatchEvent(new TreeEvent("inner")).errors;
  });
  const result = c.dispatchEvent(new TreeEvent("outer"));
  assert.deepEqual(innerErrors, [innerErr]);
  assert.deepEqual(result.errors, []);
});

test("preventDefault only counts for cancelable events", () => {
  const { c } = makeTree();
  c.addEventListener("a", (e) => e.preventDefault());
  c.addEventListener("b", (e) => e.preventDefault());
  const plain = c.dispatchEvent(new TreeEvent("a"));
  const cancelable = c.dispatchEvent(new TreeEvent("b", { cancelable: true }));
  assert.equal(plain.canceled, false);
  assert.equal(cancelable.canceled, true);
});

test("duplicate registration of the same listener is ignored", () => {
  const { c } = makeTree();
  const log: string[] = [];
  const l = logger(log, "hit");
  c.addEventListener("hit", l);
  c.addEventListener("hit", l);
  c.addEventListener("hit", l, false);
  c.dispatchEvent(new TreeEvent("hit"));
  assert.deepEqual(log, ["hit"]);
});

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) {
  console.error("failing:", failures.join(", "));
  process.exit(1);
}
