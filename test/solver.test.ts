import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { MAX_EVENTS, solve } from "../src/solver.ts";
import { InvalidInputError } from "../src/types.ts";
import type { Problem, SolveResult } from "../src/types.ts";

// ---------- reference implementation: brute force over all permutations ----------

function isValidOrder(problem: Problem, order: string[]): boolean {
  const pos = new Map(order.map((id, index) => [id, index]));
  for (const [before, after] of problem.precedence ?? []) {
    if (pos.get(before)! >= pos.get(after)!) return false;
  }
  const hasCreate = new Set(problem.events.filter((e) => e.op === "create").map((e) => e.object));
  const byId = new Map(problem.events.map((e) => [e.id, e]));
  const state = new Map<string, "absent" | "present" | "deleted">();
  for (const id of order) {
    const event = byId.get(id)!;
    const current = state.get(event.object) ?? (hasCreate.has(event.object) ? "absent" : "present");
    if (event.op === "create") {
      if (current !== "absent") return false;
      state.set(event.object, "present");
    } else if (event.op === "update") {
      if (current !== "present") return false;
    } else {
      if (current !== "present") return false;
      state.set(event.object, "deleted");
    }
  }
  return true;
}

function nextPermutation(a: string[]): boolean {
  let i = a.length - 2;
  while (i >= 0 && a[i] >= a[i + 1]) i -= 1;
  if (i < 0) return false;
  let j = a.length - 1;
  while (a[j] <= a[i]) j -= 1;
  [a[i], a[j]] = [a[j], a[i]];
  let lo = i + 1;
  let hi = a.length - 1;
  while (lo < hi) {
    [a[lo], a[hi]] = [a[hi], a[lo]];
    lo += 1;
    hi -= 1;
  }
  return true;
}

function bruteForce(problem: Problem): string[] | null {
  const perm = problem.events.map((e) => e.id).sort();
  if (perm.length === 0) return [];
  do {
    if (isValidOrder(problem, perm)) return [...perm];
  } while (nextPermutation(perm));
  return null;
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// ---------- targeted cases ----------

test("update before create is reordered (constraints checked jointly, not topo-first)", () => {
  const result = solve({
    events: [
      { id: "a", object: "x", op: "update" },
      { id: "b", object: "x", op: "create" },
    ],
  });
  // A plain lexicographic topological sort would emit ["a", "b"] and then
  // declare the instance unsolvable; the joint search must return ["b", "a"].
  assert.deepEqual(result, { ok: true, order: ["b", "a"] });
});

test("lexicographically smallest among all valid orders", () => {
  const result = solve({
    events: [
      { id: "a", object: "x", op: "update" },
      { id: "b", object: "y", op: "update" },
      { id: "c", object: "x", op: "create" },
    ],
  });
  assert.deepEqual(result, { ok: true, order: ["b", "c", "a"] });
});

test("delete stays after create and updates of the same object", () => {
  const result = solve({
    events: [
      { id: "c", object: "x", op: "create" },
      { id: "d", object: "x", op: "delete" },
      { id: "u", object: "x", op: "update" },
    ],
  });
  assert.deepEqual(result, { ok: true, order: ["c", "u", "d"] });
});

test("precedence constraints interact with state constraints", () => {
  const result = solve({
    events: [
      { id: "a", object: "x", op: "create" },
      { id: "b", object: "y", op: "update" },
      { id: "c", object: "x", op: "delete" },
      { id: "d", object: "y", op: "delete" },
    ],
    precedence: [
      ["c", "b"],
      ["b", "d"],
    ],
  });
  assert.equal(result.ok, true);
  if (result.ok) assert.ok(isValidOrder({ events: [
      { id: "a", object: "x", op: "create" },
      { id: "b", object: "y", op: "update" },
      { id: "c", object: "x", op: "delete" },
      { id: "d", object: "y", op: "delete" },
    ], precedence: [["c", "b"], ["b", "d"]] }, result.order));
  if (result.ok) assert.deepEqual(result.order, ["a", "c", "b", "d"]);
});

test("precedence cycle reports blocking prefix and cycle", () => {
  const result = solve({
    events: [
      { id: "a", object: "x", op: "update" },
      { id: "b", object: "x", op: "update" },
    ],
    precedence: [
      ["a", "b"],
      ["b", "a"],
    ],
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.deepEqual(result.blockingPrefix, []);
    assert.deepEqual(result.precedenceCycle, ["a", "b"]);
    assert.deepEqual(result.constraints.precedence, [
      ["b", "a"],
      ["a", "b"],
    ]);
    assert.deepEqual(result.remainingEvents, ["a", "b"]);
  }
});

test("precedence forcing delete before create is unsolvable with state diagnostics", () => {
  const result = solve({
    events: [
      { id: "c", object: "x", op: "create" },
      { id: "d", object: "x", op: "delete" },
    ],
    precedence: [["d", "c"]],
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.deepEqual(result.blockingPrefix, []);
    const blockedD = result.blocked.find((b) => b.event === "d")!;
    assert.equal(blockedD.unmetPredecessors.length, 0);
    assert.match(blockedD.stateViolation!, /does not exist yet/);
    const blockedC = result.blocked.find((b) => b.event === "c")!;
    assert.deepEqual(blockedC.unmetPredecessors, ["d"]);
    assert.equal(result.constraints.state.length, 1);
  }
});

test("update stranded after delete reports the blocking prefix", () => {
  const result = solve({
    events: [
      { id: "c", object: "x", op: "create" },
      { id: "d", object: "x", op: "delete" },
      { id: "u", object: "x", op: "update" },
    ],
    precedence: [["d", "u"]],
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.deepEqual(result.blockingPrefix, ["c", "d"]);
    assert.deepEqual(result.remainingEvents, ["u"]);
    assert.match(result.blocked[0].stateViolation!, /deleted by event "d"/);
    assert.deepEqual(result.constraints.state, [
      'event "u": object "x" was deleted by event "d"',
    ]);
  }
});

test("duplicate create on the same object is unsolvable", () => {
  const result = solve({
    events: [
      { id: "a", object: "x", op: "create" },
      { id: "b", object: "x", op: "create" },
    ],
  });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.deepEqual(result.blockingPrefix, ["a"]);
    assert.deepEqual(result.remainingEvents, ["b"]);
    assert.match(result.blocked[0].stateViolation!, /already exists/);
  }
});

test("empty problem yields an empty order", () => {
  assert.deepEqual(solve({ events: [] }), { ok: true, order: [] });
});

test("maximum size instance solves quickly", () => {
  const events = [];
  for (let i = 1; i <= MAX_EVENTS; i += 1) {
    events.push({ id: `e${String(i).padStart(2, "0")}`, object: `o${i % 4}`, op: "update" as const });
  }
  const precedence: Array<[string, string]> = [];
  for (let i = 1; i < MAX_EVENTS; i += 1) {
    precedence.push([events[i].id, events[i - 1].id]); // force reverse id order
  }
  const result = solve({ events, precedence });
  assert.equal(result.ok, true);
  if (result.ok) {
    assert.deepEqual(result.order, events.map((e) => e.id).reverse());
  }
});

test("input validation", () => {
  assert.throws(
    () => solve({ events: Array.from({ length: MAX_EVENTS + 1 }, (_, i) => ({ id: `e${i}`, object: "x", op: "update" as const })) }),
    InvalidInputError,
  );
  assert.throws(
    () => solve({ events: [{ id: "a", object: "x", op: "update" }, { id: "a", object: "x", op: "update" }] }),
    /duplicate event id/,
  );
  assert.throws(
    () => solve({ events: [{ id: "a", object: "x", op: "update" }], precedence: [["a", "b"]] }),
    /invalid precedence pair/,
  );
  assert.throws(
    () => solve({ events: [{ id: "a", object: "x", op: "noop" as never }] }),
    /unknown op/,
  );
});

// ---------- randomized cross-check against brute force ----------

test("fuzz: solver matches brute force on random small instances", () => {
  const rand = mulberry32(20261005);
  const ops = ["create", "update", "delete"] as const;
  for (let iter = 0; iter < 400; iter += 1) {
    const n = 2 + Math.floor(rand() * 7); // 2..8 events
    const numObjects = 1 + Math.floor(rand() * 3);
    const events = Array.from({ length: n }, (_, i) => ({
      id: String.fromCharCode(97 + i),
      object: `obj${Math.floor(rand() * numObjects)}`,
      op: ops[Math.floor(rand() * ops.length)],
    }));
    const precedence: Array<[string, string]> = [];
    for (const event of events) {
      for (const other of events) {
        if (rand() < 0.12) precedence.push([event.id, other.id]);
      }
    }
    const problem: Problem = { events, precedence };
    const expected = bruteForce(problem);
    const result = solve(problem);
    assert.equal(result.ok, expected !== null, `solvability mismatch on ${JSON.stringify(problem)}`);
    if (result.ok) {
      assert.deepEqual(result.order, expected, `order mismatch on ${JSON.stringify(problem)}`);
      assert.ok(isValidOrder(problem, result.order));
    } else {
      checkBlockingReport(problem, result);
    }
  }
});

function checkBlockingReport(problem: Problem, result: SolveResult & { ok: false }): void {
  // Prefix and remaining events partition the whole event set.
  const all = [...result.blockingPrefix, ...result.remainingEvents].sort();
  assert.deepEqual(all, problem.events.map((e) => e.id).sort());
  // The blocking prefix itself is locally valid.
  assert.ok(isValidPrefix(problem, result.blockingPrefix));
  // Every remaining event is blocked by precedence and/or state.
  for (const entry of result.blocked) {
    assert.ok(
      entry.unmetPredecessors.length > 0 || entry.stateViolation !== null,
      `event ${entry.event} reported as blocked without a reason`,
    );
  }
}

function isValidPrefix(problem: Problem, prefix: string[]): boolean {
  const emitted = new Set(prefix);
  for (const [before, after] of problem.precedence ?? []) {
    if (emitted.has(after) && !emitted.has(before)) return false;
  }
  const hasCreate = new Set(problem.events.filter((e) => e.op === "create").map((e) => e.object));
  const byId = new Map(problem.events.map((e) => [e.id, e]));
  const state = new Map<string, string>();
  for (const id of prefix) {
    const event = byId.get(id)!;
    const current = state.get(event.object) ?? (hasCreate.has(event.object) ? "absent" : "present");
    if (event.op === "create") {
      if (current !== "absent") return false;
      state.set(event.object, "present");
    } else if (event.op === "update") {
      if (current !== "present") return false;
    } else {
      if (current !== "present") return false;
      state.set(event.object, "deleted");
    }
  }
  return true;
}

// ---------- CLI smoke tests ----------

const cliPath = fileURLToPath(new URL("../src/cli.ts", import.meta.url));

function runCli(args: string[], input?: string) {
  return spawnSync(process.execPath, ["--experimental-strip-types", cliPath, ...args], {
    encoding: "utf8",
    input,
  });
}

test("cli solves a problem from a json file", () => {
  const dir = mkdtempSync(join(tmpdir(), "reorderer-"));
  const file = join(dir, "in.json");
  writeFileSync(
    file,
    JSON.stringify({
      events: [
        { id: "a", object: "x", op: "update" },
        { id: "b", object: "x", op: "create" },
      ],
    }),
  );
  const run = runCli([file]);
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(JSON.parse(run.stdout), { ok: true, order: ["b", "a"] });
});

test("cli exits 1 with a blocking report when unsolvable", () => {
  const run = runCli(
    [],
    JSON.stringify({
      events: [
        { id: "c", object: "x", op: "create" },
        { id: "d", object: "x", op: "delete" },
      ],
      precedence: [["d", "c"]],
    }),
  );
  assert.equal(run.status, 1, run.stderr);
  const report = JSON.parse(run.stdout);
  assert.equal(report.ok, false);
  assert.deepEqual(report.remainingEvents, ["c", "d"]);
});

test("cli exits 2 on invalid input", () => {
  const run = runCli([], JSON.stringify({ events: [{ id: "a", object: "x", op: "boom" }] }));
  assert.equal(run.status, 2);
  assert.match(run.stderr, /unknown op/);
});
