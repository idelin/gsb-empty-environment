import assert from "node:assert/strict";
import test from "node:test";
import {
  analyze,
  checkSafetyProof,
  replayPrefix,
  verifySafetyProof,
  verifyWitness,
  type EventSpec,
  type OperationGraph,
  type SafetyProof,
} from "../src/index.ts";

test("complete objects reject every read while locked", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "complete",
          contentLabel: "complete",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-lock", op: { kind: "lock" }, before: ["2-open"] },
      { id: "4-read", op: { kind: "read", handle: "h" }, before: ["3-lock"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  if (result.status !== "safe") {
    return;
  }
  assert.equal(verifySafetyProof(graph, result.proof).ok, true);
  const replay = replayPrefix(graph, ["1-create", "2-open", "3-lock", "4-read"]);
  assert.equal(replay.steps.at(-1)?.outcome, "rejected");
  assert.equal(replay.steps.at(-1)?.rejection?.code, "locked-complete");
});

test("untilLock admits only handles opened before the current lock", () => {
  const allowed: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "untilLock",
          contentLabel: "complete",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-lock", op: { kind: "lock" }, before: ["2-open"] },
      { id: "4-read", op: { kind: "read", handle: "h" }, before: ["3-lock"] },
    ],
  };
  const result = analyze(allowed);
  assert.equal(result.status, "unsafe");
  if (result.status === "unsafe") {
    assert.deepEqual(result.prefix, ["1-create", "2-open", "3-lock", "4-read"]);
    assert.equal(verifyWitness(allowed, result.witness).ok, true);
  }

  const denied: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "untilLock",
          contentLabel: "complete",
        },
      },
      { id: "2-lock", op: { kind: "lock" }, before: ["1-create"] },
      { id: "3-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["2-lock"] },
      { id: "4-read", op: { kind: "read", handle: "h" }, before: ["3-open"] },
    ],
  };
  const deniedResult = analyze(denied);
  assert.equal(deniedResult.status, "safe");
  const replay = replayPrefix(denied, ["1-create", "2-lock", "3-open", "4-read"]);
  assert.equal(replay.steps.at(-1)?.rejection?.code, "locked-until-lock-handle");
});

test("a second lock uses the new lock instant", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "untilLock",
          contentLabel: "public",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h1" }, before: ["1-create"] },
      { id: "3-lock", op: { kind: "lock" }, before: ["2-open"] },
      { id: "4-unlock", op: { kind: "unlock" }, before: ["3-lock"] },
      {
        id: "5-open-mid",
        op: { kind: "open", path: "/a", handle: "mid" },
        before: ["4-unlock"],
      },
      {
        id: "6-lock2",
        op: { kind: "lock" },
        before: ["4-unlock", "5-open-mid"],
      },
      { id: "7-read-h1", op: { kind: "read", handle: "h1" }, before: ["6-lock2"] },
      {
        id: "8-read-mid",
        op: { kind: "read", handle: "mid" },
        before: ["6-lock2"],
      },
      {
        id: "9-open-late",
        op: { kind: "open", path: "/a", handle: "late" },
        before: ["6-lock2"],
      },
      {
        id: "10-read-late",
        op: { kind: "read", handle: "late" },
        before: ["9-open-late"],
      },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  const replay = replayPrefix(graph, [
    "1-create",
    "2-open",
    "3-lock",
    "4-unlock",
    "5-open-mid",
    "6-lock2",
    "7-read-h1",
    "8-read-mid",
    "9-open-late",
    "10-read-late",
  ]);
  assert.equal(replay.steps[6]?.outcome, "applied");
  assert.equal(replay.steps[6]?.rejection, undefined);
  assert.equal(replay.steps[7]?.outcome, "applied");
  assert.equal(replay.steps[7]?.rejection, undefined);
  assert.equal(replay.steps[9]?.outcome, "rejected");
  assert.equal(replay.steps[9]?.rejection?.code, "locked-until-lock-handle");
});

test("restore creates a new object while the old handle remains attached", () => {
  const graph: OperationGraph = {
    initial: [
      {
        path: "/a",
        protectionClass: "public",
        contentLabel: "public",
      },
    ],
    events: [
      { id: "1-open-old", op: { kind: "open", path: "/a", handle: "old" } },
      {
        id: "2-restore",
        op: {
          kind: "restore",
          path: "/a",
          protectionClass: "public",
          contentLabel: "complete",
        },
        before: ["1-open-old"],
      },
      {
        id: "3-open-new",
        op: { kind: "open", path: "/a", handle: "fresh" },
        before: ["2-restore"],
      },
      {
        id: "4-lock",
        op: { kind: "lock" },
        before: ["1-open-old", "3-open-new"],
      },
      {
        id: "5-read-old",
        op: { kind: "read", handle: "old" },
        before: ["4-lock"],
      },
      {
        id: "6-read-new",
        op: { kind: "read", handle: "fresh" },
        before: ["4-lock"],
      },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "unsafe");
  if (result.status === "unsafe") {
    assert.deepEqual(result.prefix, [
      "1-open-old",
      "2-restore",
      "3-open-new",
      "4-lock",
      "6-read-new",
    ]);
    assert.equal(result.violation.protectionClass, "public");
    assert.equal(result.violation.contentLabel, "complete");
    assert.equal(verifyWitness(graph, result.witness).ok, true);
  }

  const replay = replayPrefix(graph, [
    "1-open-old",
    "2-restore",
    "3-open-new",
    "4-lock",
    "5-read-old",
  ]);
  assert.equal(replay.steps.at(-1)?.outcome, "applied");
  assert.equal(replay.violation, undefined);
});

test("unlink and replace do not revoke already-open handles", () => {
  const graph: OperationGraph = {
    initial: [{ path: "/a", protectionClass: "public", contentLabel: "public" }],
    events: [
      { id: "1-open", op: { kind: "open", path: "/a", handle: "h" } },
      { id: "2-unlink", op: { kind: "unlink", path: "/a" }, before: ["1-open"] },
      {
        id: "3-read",
        op: { kind: "read", handle: "h", path: "/a" },
        before: ["2-unlink"],
      },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  const replay = replayPrefix(graph, ["1-open", "2-unlink", "3-read"]);
  assert.equal(replay.steps.at(-1)?.outcome, "applied");
});

test("copy reads through the handle and preserves the source content label", () => {
  const graph: OperationGraph = {
    volumes: {
      source: "untilLock",
      target: "public",
    },
    pathVolumes: { "/src": "source", "/dst": "target" },
    initial: [
      {
        path: "/src",
        protectionClass: "untilLock",
        contentLabel: "complete",
      },
    ],
    events: [
      { id: "1-open-src", op: { kind: "open", path: "/src", handle: "src" } },
      {
        id: "2-copy",
        op: { kind: "copy", handle: "src", to: "/dst" },
        before: ["1-open-src"],
      },
      {
        id: "3-open-dst",
        op: { kind: "open", path: "/dst", handle: "dst" },
        before: ["2-copy"],
      },
      {
        id: "4-lock",
        op: { kind: "lock" },
        before: ["1-open-src", "3-open-dst"],
      },
      {
        id: "5-read-dst",
        op: { kind: "read", handle: "dst" },
        before: ["4-lock"],
      },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "unsafe");
  if (result.status === "unsafe") {
    assert.equal(result.violation.contentLabel, "complete");
    assert.equal(result.violation.protectionClass, "public");
  }
});

test("a hard-link alias cannot lower the original protection class", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "complete",
          contentLabel: "complete",
        },
      },
      { id: "2-link", op: { kind: "link", from: "/a", to: "/b" }, before: ["1-create"] },
      {
        id: "3-downgrade",
        op: { kind: "setClass", path: "/b", class: "public" },
        before: ["2-link"],
      },
      { id: "4-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["3-downgrade"] },
      { id: "5-lock", op: { kind: "lock" }, before: ["4-open"] },
      { id: "6-read", op: { kind: "read", handle: "h" }, before: ["5-lock"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  const replay = replayPrefix(graph, [
    "1-create",
    "2-link",
    "3-downgrade",
    "4-open",
    "5-lock",
    "6-read",
  ]);
  assert.equal(replay.steps[2]?.rejection?.code, "alias-downgrade");
  assert.equal(replay.steps[5]?.rejection?.code, "locked-complete");
});

test("a class change through another path affects the same object", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "public",
          contentLabel: "public",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-rename", op: { kind: "rename", from: "/a", to: "/b" }, before: ["2-open"] },
      {
        id: "4-upgrade",
        op: { kind: "setClass", path: "/b", class: "complete" },
        before: ["3-rename"],
      },
      { id: "5-lock", op: { kind: "lock" }, before: ["4-upgrade"] },
      { id: "6-read", op: { kind: "read", handle: "h" }, before: ["5-lock"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  const replay = replayPrefix(graph, [
    "1-create",
    "2-open",
    "3-rename",
    "4-upgrade",
    "5-lock",
    "6-read",
  ]);
  assert.equal(replay.steps[3]?.outcome, "applied");
  assert.equal(replay.steps[5]?.rejection?.code, "locked-complete");
});

test("failed preconditions have zero side effects and later events continue", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-open-missing",
        op: { kind: "open", path: "/missing", handle: "h" },
      },
      { id: "2-read", op: { kind: "read", handle: "h" }, before: ["1-open-missing"] },
      {
        id: "3-create",
        op: {
          kind: "create",
          path: "/later",
          protectionClass: "public",
          contentLabel: "public",
        },
        before: ["2-read"],
      },
      { id: "4-read-later", op: { kind: "readPath", path: "/later" }, before: ["3-create"] },
    ],
  };
  const replay = replayPrefix(graph, [
    "1-open-missing",
    "2-read",
    "3-create",
    "4-read-later",
  ]);
  assert.equal(replay.steps[0]?.rejection?.code, "path-missing");
  assert.equal(replay.steps[1]?.rejection?.code, "handle-missing");
  assert.equal(replay.steps[2]?.outcome, "applied");
  assert.equal(replay.steps[3]?.outcome, "applied");
  assert.equal(replay.state.paths.has("/missing"), false);
  assert.equal(replay.state.paths.has("/later"), true);
});

test("denied copy creates no destination object", () => {
  const graph: OperationGraph = {
    initial: [
      {
        path: "/src",
        protectionClass: "complete",
        contentLabel: "complete",
      },
    ],
    events: [
      { id: "1-open", op: { kind: "open", path: "/src", handle: "src" } },
      { id: "2-lock", op: { kind: "lock" }, before: ["1-open"] },
      {
        id: "3-copy",
        op: { kind: "copy", handle: "src", to: "/dst" },
        before: ["2-lock"],
      },
      { id: "4-read-dst", op: { kind: "readPath", path: "/dst" }, before: ["3-copy"] },
    ],
  };
  const replay = replayPrefix(graph, ["1-open", "2-lock", "3-copy", "4-read-dst"]);
  assert.equal(replay.steps[2]?.rejection?.code, "locked-complete");
  assert.equal(replay.steps[3]?.rejection?.code, "path-missing");
  assert.equal(replay.state.paths.has("/dst"), false);
});

test("the closure checker rejects a forged proof with one feasible edge removed", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "public",
          contentLabel: "public",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-read", op: { kind: "read", handle: "h" }, before: ["2-open"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  if (result.status !== "safe") {
    return;
  }
  assert.equal(verifySafetyProof(graph, result.proof).ok, true);
  const forged = structuredClone(result.proof) as unknown as {
    components: Array<{ transitions: unknown[] }>;
  };
  const component = forged.components[0];
  assert.ok(component);
  component.transitions.splice(0, 1);
  const checked = checkSafetyProof(graph, forged as unknown as SafetyProof);
  assert.equal(checked.ok, false);
  assert.match(checked.errors.join("\n"), /missing enabled edge|never checks/);
});

test("a proof survives a JSON round trip before independent checking", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "public",
          contentLabel: "public",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-read", op: { kind: "read", handle: "h" }, before: ["2-open"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  if (result.status !== "safe") {
    return;
  }
  const shipped = JSON.parse(JSON.stringify(result.proof)) as SafetyProof;
  assert.equal(verifySafetyProof(graph, shipped).ok, true);
});

test("the checker rejects a proof for a graph with a deleted dependency edge", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "public",
          contentLabel: "public",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-read", op: { kind: "read", handle: "h" }, before: ["2-open"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  if (result.status !== "safe") {
    return;
  }
  const changed: OperationGraph = {
    ...graph,
    events: graph.events.map((event, index) =>
      index === 1 ? { ...event, before: [] } : event,
    ),
  };
  const checked = verifySafetyProof(changed, result.proof);
  assert.equal(checked.ok, false);
  assert.match(checked.errors.join("\n"), /digest/);
});

test("budget exhaustion returns unknown", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "1-create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "public",
          contentLabel: "public",
        },
      },
      { id: "2-open", op: { kind: "open", path: "/a", handle: "h" }, before: ["1-create"] },
      { id: "3-read", op: { kind: "read", handle: "h" }, before: ["2-open"] },
    ],
  };
  const result = analyze(graph, { maxStates: 1 });
  assert.equal(result.status, "unknown");
  if (result.status === "unknown") {
    assert.equal(result.reason, "budget");
  }
  const nested = analyze(graph, { budget: { maxStates: 1 } });
  assert.equal(nested.status, "unknown");
});

test("a cyclic partial order is invalid", () => {
  const graph: OperationGraph = {
    events: [
      { id: "a", op: { kind: "lock" }, before: ["b"] },
      { id: "b", op: { kind: "unlock" }, before: ["a"] },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "invalid");
  if (result.status === "invalid") {
    assert.match(result.reason, /cycle/);
  }
});

test("numeric event ids are normalized for dictionary order", () => {
  const graph: OperationGraph = {
    initial: [
      {
        path: "/a",
        protectionClass: "untilLock",
        contentLabel: "complete",
      },
    ],
    events: [
      { id: 10 as unknown as string, op: { kind: "open", path: "/a", handle: "h" } },
      { id: 2 as unknown as string, op: { kind: "lock" } },
      { id: 1 as unknown as string, op: { kind: "read", handle: "h" } },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "unsafe");
  if (result.status === "unsafe") {
    assert.deepEqual(result.prefix, ["10", "2", "1"]);
  }
});

test("after and deps aliases are folded into the same partial order", () => {
  const graph: OperationGraph = {
    events: [
      {
        id: "create",
        op: {
          kind: "create",
          path: "/a",
          protectionClass: "public",
          contentLabel: "public",
        },
        after: ["open"],
      },
      {
        id: "open",
        op: { kind: "open", path: "/a", handle: "h" },
        deps: ["create"],
      },
      {
        id: "read",
        op: { kind: "read", handle: "h" },
        deps: ["open"],
      },
    ],
  };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  const replay = replayPrefix(graph, ["create", "open", "read"]);
  assert.deepEqual(
    replay.steps.map((step) => step.outcome),
    ["applied", "applied", "applied"],
  );
});

test("24 operations over eight independent files compose safely", () => {
  const events: EventSpec[] = [];
  for (let file = 0; file < 8; file += 1) {
    const path = `/f${file}`;
    events.push({
      id: `a-create-${file}`,
      op: {
        kind: "create" as const,
        path,
        protectionClass: "public" as const,
        contentLabel: "public" as const,
      },
    });
    events.push({
      id: `b-open-${file}`,
      op: { kind: "open" as const, path, handle: `h${file}` },
      before: [`a-create-${file}`],
    });
    events.push({
      id: `c-read-${file}`,
      op: { kind: "read" as const, handle: `h${file}` },
      before: [`b-open-${file}`],
    });
  }
  const graph: OperationGraph = { events };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  if (result.status === "safe") {
    assert.equal(result.explored.components, 8);
    assert.equal(verifySafetyProof(graph, result.proof).ok, true);
  }
});
