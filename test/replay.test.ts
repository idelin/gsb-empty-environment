import assert from "node:assert/strict";
import test from "node:test";
import { Executor, replay } from "../src/executor.ts";
import { editorSpecV1 } from "../src/editorSpec.ts";
import { dialogTree } from "../src/fixtures.ts";
import { nodeIds } from "../src/layout.ts";
import type { Ev } from "../src/spec.ts";

const ids = nodeIds(dialogTree);
const tree = { has: (id: string) => ids.has(id) };

const events: Ev[] = [
  { type: "focus", target: "title" },
  { type: "type", payload: { ch: "a" }, target: "status" },
  { type: "save", target: "btn-ok" },
];

test("replay of recorded events matches the original run", () => {
  const spec = editorSpecV1();
  const live = new Executor(spec);
  live.attachNodeTree(tree);
  live.registerEffect("persist", () => {});
  for (const e of events) live.dispatch(e);

  const report = replay(spec, events, {
    recorded: live.journal,
    nodeTree: tree,
    effects: new Map([["persist", () => {}]]),
  });
  assert.equal(report.divergences.length, 0);
  assert.equal(report.finalHash, live.hash);
});

test("missing target node during replay is reported as divergence", () => {
  const spec = editorSpecV1();
  const live = new Executor(spec);
  live.attachNodeTree(tree);
  live.registerEffect("persist", () => {});
  for (const e of events) live.dispatch(e);

  const shrunk = { has: (id: string) => id !== "btn-ok" }; // 节点树缺 btn-ok
  const report = replay(spec, events, {
    recorded: live.journal,
    nodeTree: shrunk,
    effects: new Map([["persist", () => {}]]),
  });
  assert.equal(report.divergences.length, 1);
  assert.match(report.divergences[0]!.reason, /btn-ok/);
  assert.equal(report.entries[2]!.status, "rejected");
  assert.equal(report.entries[2]!.diagnostics[0]!.code, "missing-target");
});

test("replay without recorded journal still flags missing targets", () => {
  const spec = editorSpecV1();
  const report = replay(spec, [{ type: "focus", target: "ghost" }], { nodeTree: tree });
  assert.equal(report.divergences.length, 1);
  assert.match(report.divergences[0]!.reason, /ghost/);
});

test("replay is deterministic across runs", () => {
  const spec = editorSpecV1();
  const a = replay(spec, events, { nodeTree: tree, effects: new Map([["persist", () => {}]]) });
  const b = replay(spec, events, { nodeTree: tree, effects: new Map([["persist", () => {}]]) });
  assert.equal(a.finalHash, b.finalHash);
  assert.deepEqual(a.entries, b.entries);
});
