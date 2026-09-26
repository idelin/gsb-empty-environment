import assert from "node:assert/strict";
import test from "node:test";
import { Executor } from "../src/executor.ts";
import { editorSpecV1, MAX_LINES } from "../src/editorSpec.ts";
import type { Ev } from "../src/spec.ts";

const seq: Ev[] = [
  { type: "focus" },
  { type: "type", payload: { ch: "a" } },
  { type: "type", payload: { ch: "b" } },
  { type: "save" },
];

test("applies transitions and tracks hierarchical path", () => {
  const ex = new Executor(editorSpecV1());
  ex.registerEffect("persist", () => {});
  assert.deepEqual(ex.getConfig().path, ["root", "active", "idle"]);
  ex.dispatch({ type: "focus" });
  assert.deepEqual(ex.getConfig().path, ["root", "active", "editing", "clean"]);
  ex.dispatch({ type: "type", payload: { ch: "a" } });
  const cfg = ex.getConfig();
  assert.deepEqual(cfg.path, ["root", "active", "editing", "dirty"]);
  assert.equal(cfg.data.text, "a");
  assert.equal(cfg.data.lines, 1);
  assert.equal(cfg.data.saved, false);
});

test("illegal events keep diagnostics and leave state unchanged", () => {
  const ex = new Executor(editorSpecV1());
  const before = ex.getConfig();
  const r1 = ex.dispatch({ type: "type", payload: { ch: "x" } }); // idle 下无 type
  assert.equal(r1.status, "rejected");
  assert.equal(r1.diagnostics[0]?.code, "no-transition");
  assert.deepEqual(ex.getConfig(), before);

  ex.dispatch({ type: "lock" });
  const locked = ex.getConfig();
  const r2 = ex.dispatch({ type: "focus" }); // locked 下不可 focus
  assert.equal(r2.status, "rejected");
  assert.deepEqual(ex.getConfig(), locked);
});

test("guard failure rejects without changing state", () => {
  const ex = new Executor(editorSpecV1());
  ex.dispatch({ type: "focus" });
  for (let i = 0; i < MAX_LINES; i++) ex.dispatch({ type: "type", payload: { ch: "a" } });
  const full = ex.getConfig();
  const r = ex.dispatch({ type: "type", payload: { ch: "a" } });
  assert.equal(r.status, "rejected");
  assert.equal(r.diagnostics[0]?.code, "guard-failed");
  assert.deepEqual(ex.getConfig(), full);
});

test("same initial state and event sequence give identical results", () => {
  const run = () => {
    const ex = new Executor(editorSpecV1());
    ex.registerEffect("persist", () => {});
    for (const e of seq) ex.dispatch(e);
    return { hash: ex.hash, journal: ex.journal.map((j) => [j.status, j.afterHash]) };
  };
  assert.deepEqual(run(), run());
});

test("side-effect failure rolls back the whole transaction", () => {
  const ex = new Executor(editorSpecV1());
  ex.registerEffect("persist", () => {
    throw new Error("disk full");
  });
  ex.dispatch({ type: "focus" });
  ex.dispatch({ type: "type", payload: { ch: "a" } });
  const before = ex.getConfig();
  const r = ex.dispatch({ type: "save" });
  assert.equal(r.status, "rolled-back");
  assert.equal(r.diagnostics[0]?.code, "effect-failed");
  assert.match(r.diagnostics[0]!.message, /disk full/);
  assert.deepEqual(ex.getConfig(), before); // 状态回滚到 save 之前
});

test("unregistered effect also rolls back", () => {
  const ex = new Executor(editorSpecV1());
  ex.dispatch({ type: "focus" });
  ex.dispatch({ type: "type", payload: { ch: "a" } });
  const before = ex.getConfig();
  const r = ex.dispatch({ type: "save" });
  assert.equal(r.status, "rolled-back");
  assert.match(r.diagnostics[0]!.message, /not registered/);
  assert.deepEqual(ex.getConfig(), before);
});

test("journal records every dispatch with hashes", () => {
  const ex = new Executor(editorSpecV1());
  ex.registerEffect("persist", () => {});
  for (const e of seq) ex.dispatch(e);
  assert.equal(ex.journal.length, seq.length);
  for (let i = 1; i < ex.journal.length; i++) {
    assert.equal(ex.journal[i]!.beforeHash, ex.journal[i - 1]!.afterHash);
  }
  assert.equal(ex.journal.at(-1)!.afterHash, ex.hash);
});
