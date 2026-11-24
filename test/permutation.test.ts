import assert from "node:assert/strict";
import test from "node:test";
import {
  analyze,
  verifySafetyProof,
  type EventSpec,
  type Operation,
  type OperationGraph,
} from "../src/index.ts";
import { bruteForce } from "./helpers.ts";

function compareSequences(
  left: readonly string[],
  right: readonly string[],
): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    if (left[index] !== right[index]) {
      return (left[index] as string) < (right[index] as string) ? -1 : 1;
    }
  }
  return left.length - right.length;
}

test("full permutations agree with the analyzer on a small branching graph", () => {
  const graph: OperationGraph = {
    initial: [
      {
        path: "/a",
        protectionClass: "untilLock",
        contentLabel: "complete",
      },
    ],
    events: [
      { id: "a-open", op: { kind: "open", path: "/a", handle: "h1" } },
      { id: "b-open", op: { kind: "open", path: "/a", handle: "h2" } },
      { id: "c-lock", op: { kind: "lock" } },
      { id: "d-read", op: { kind: "read", handle: "h1" } },
      { id: "e-read", op: { kind: "read", handle: "h2" } },
    ],
  };
  const expected = bruteForce(graph);
  const actual = analyze(graph);
  assert.equal(actual.status, "unsafe");
  if (actual.status === "unsafe") {
    assert.deepEqual(actual.prefix, expected.prefix);
    assert.deepEqual(actual.prefix, ["a-open", "c-lock", "d-read"]);
  }
});

test("full permutations agree on a small restore graph", () => {
  const graph: OperationGraph = {
    initial: [
      {
        path: "/a",
        protectionClass: "public",
        contentLabel: "public",
      },
    ],
    events: [
      { id: "a-open-old", op: { kind: "open", path: "/a", handle: "old" } },
      {
        id: "b-restore",
        op: {
          kind: "restore",
          path: "/a",
          protectionClass: "public",
          contentLabel: "complete",
        },
        before: ["a-open-old"],
      },
      {
        id: "c-open-new",
        op: { kind: "open", path: "/a", handle: "fresh" },
        before: ["b-restore"],
      },
      { id: "d-lock", op: { kind: "lock" } },
      { id: "e-read-old", op: { kind: "read", handle: "old" }, before: ["a-open-old"] },
      { id: "f-read-new", op: { kind: "read", handle: "fresh" }, before: ["c-open-new"] },
    ],
  };
  const expected = bruteForce(graph);
  const actual = analyze(graph);
  assert.equal(actual.status, "unsafe");
  if (actual.status === "unsafe") {
    assert.deepEqual(actual.prefix, expected.prefix);
    assert.equal(compareSequences(actual.prefix, ["a-open-old", "b-restore", "c-open-new", "d-lock", "f-read-new"]), 0);
  }
});

test("full permutations agree on a safe graph", () => {
  const graph: OperationGraph = {
    initial: [{ path: "/a", protectionClass: "public", contentLabel: "public" }],
    events: [
      { id: "a-open", op: { kind: "open", path: "/a", handle: "h1" } },
      { id: "b-lock", op: { kind: "lock" } },
      { id: "c-open", op: { kind: "open", path: "/a", handle: "h2" }, before: ["b-lock"] },
      { id: "d-read", op: { kind: "read", handle: "h2" }, before: ["c-open"] },
      { id: "e-unlock", op: { kind: "unlock" }, before: ["b-lock"] },
    ],
  };
  const expected = bruteForce(graph);
  const actual = analyze(graph);
  assert.equal(expected.prefix, undefined);
  assert.equal(actual.status, "safe");
  if (actual.status === "safe") {
    assert.equal(verifySafetyProof(graph, actual.proof).ok, true);
  }
});

test("lock and unlock events share one global lock component", () => {
  const graph: OperationGraph = {
    initial: [
      {
        path: "/a",
        protectionClass: "untilLock",
        contentLabel: "complete",
      },
    ],
    events: [
      { id: "a-open", op: { kind: "open", path: "/a", handle: "h" } },
      { id: "b-lock", op: { kind: "lock" } },
      { id: "c-unlock", op: { kind: "unlock" } },
      { id: "d-lock", op: { kind: "lock" } },
      { id: "e-read", op: { kind: "read", handle: "h" } },
    ],
  };
  const expected = bruteForce(graph);
  const actual = analyze(graph);
  assert.equal(actual.status, "unsafe");
  if (actual.status === "unsafe") {
    assert.deepEqual(actual.prefix, expected.prefix);
  }
});

test("24 operations on independent files stay small and prove safe", () => {
  const events: EventSpec[] = Array.from({ length: 24 }, (_, index) => ({
    id: `e${String(index).padStart(2, "0")}`,
    op: {
      kind: "create" as const,
      path: `/file-${index}`,
      protectionClass: "public" as const,
      contentLabel: "public" as const,
    },
  }));
  const graph: OperationGraph = { events };
  const result = analyze(graph);
  assert.equal(result.status, "safe");
  if (result.status === "safe") {
    assert.equal(result.explored.components, 24);
    assert.ok(result.explored.states <= 48);
    assert.equal(verifySafetyProof(graph, result.proof).ok, true);
  }
});

test("more than 24 events is rejected", () => {
  const events = Array.from({ length: 25 }, (_, index) => ({
    id: `e${index}`,
    op: {
      kind: "create" as const,
      path: `/file-${index}`,
      protectionClass: "public" as const,
      contentLabel: "public" as const,
    },
  }));
  const result = analyze({ events });
  assert.equal(result.status, "invalid");
});

test("deterministic random small graphs agree with full permutation", () => {
  let seed = 0x5eed1234;
  const next = (): number => {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
    return seed;
  };
  const pick = <T>(values: readonly T[]): T =>
    values[next() % values.length] as T;
  const paths = ["/a", "/b", "/c"];
  const handles = ["h1", "h2"];
  const classes = ["public", "untilLock", "complete"] as const;

  const randomOp = (): Operation => {
    switch (next() % 14) {
      case 0:
        return {
          kind: "create",
          path: pick(paths),
          protectionClass: pick(classes),
          contentLabel: pick(classes),
        };
      case 1:
        return {
          kind: "restore",
          path: pick(paths),
          protectionClass: pick(classes),
          contentLabel: pick(classes),
        };
      case 2:
        return {
          kind: "replace",
          path: pick(paths),
          protectionClass: pick(classes),
          contentLabel: pick(classes),
        };
      case 3:
        return { kind: "open", path: pick(paths), handle: pick(handles) };
      case 4:
        return { kind: "close", handle: pick(handles) };
      case 5:
        return { kind: "read", handle: pick(handles) };
      case 6:
        return { kind: "readPath", path: pick(paths) };
      case 7:
        return { kind: "unlink", path: pick(paths) };
      case 8:
        return { kind: "rename", from: pick(paths), to: pick(paths) };
      case 9:
        return { kind: "link", from: pick(paths), to: pick(paths) };
      case 10:
        return {
          kind: "setClass",
          path: pick(paths),
          class: pick(classes),
        };
      case 11:
        return {
          kind: "copy",
          handle: pick(handles),
          to: pick(paths),
          protectionClass: pick(classes),
        };
      case 12:
        return { kind: "lock" };
      default:
        return { kind: "unlock" };
    }
  };

  for (let round = 0; round < 20; round += 1) {
    const eventCount = 4 + (next() % 2);
    const events = Array.from({ length: eventCount }, (_, index) => {
      const before: string[] = [];
      for (let earlier = 0; earlier < index; earlier += 1) {
        if (next() % 4 === 0) {
          before.push(`e${earlier}`);
        }
      }
      return {
        id: `e${index}`,
        op: randomOp(),
        before,
      };
    });
    const graph: OperationGraph = {
      initial:
        next() % 2 === 0
          ? [
              {
                path: "/a",
                protectionClass: pick(classes),
                contentLabel: pick(classes),
              },
            ]
          : [],
      events,
    };
    const expected = bruteForce(graph);
    const actual = analyze(graph);
    if (expected.prefix === undefined) {
      assert.equal(actual.status, "safe", `round ${round}`);
      if (actual.status === "safe") {
        assert.equal(verifySafetyProof(graph, actual.proof).ok, true);
      }
    } else {
      assert.equal(actual.status, "unsafe", `round ${round}`);
      if (actual.status === "unsafe") {
        assert.deepEqual(actual.prefix, expected.prefix, `round ${round}`);
      }
    }
  }
});
