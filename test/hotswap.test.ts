import assert from "node:assert/strict";
import test from "node:test";
import { Executor } from "../src/executor.ts";
import { badMigration, editorSpecV1, editorSpecV2, migrateV1toV2 } from "../src/editorSpec.ts";

test("hot-swap succeeds with valid migration evidence", () => {
  const ex = new Executor(editorSpecV1());
  ex.dispatch({ type: "focus" });
  ex.dispatch({ type: "type", payload: { ch: "a" } });

  const result = ex.hotSwap(editorSpecV2(), migrateV1toV2);
  assert.equal(result.ok, true);
  assert.deepEqual(result.errors, []);
  assert.equal(ex.spec.version, 2);
  const cfg = ex.getConfig();
  assert.equal(cfg.data.readonly, false); // 迁移补上的字段
  assert.equal(cfg.data.text, "a"); // 旧数据保留
  assert.deepEqual(cfg.path, ["root", "active", "editing", "dirty"]); // 路径保留
  // 新规约下事件继续工作
  const r = ex.dispatch({ type: "save" });
  assert.equal(r.status, "rolled-back"); // 未注册 persist，按事务回滚
});

test("failed migration keeps the old spec and state", () => {
  const ex = new Executor(editorSpecV1());
  ex.dispatch({ type: "focus" });
  const before = ex.getConfig();
  const beforeHash = ex.hash;

  const result = ex.hotSwap(editorSpecV2(), badMigration);
  assert.equal(result.ok, false);
  assert.ok(result.errors.some((e) => e.includes("nonexistent")));
  assert.equal(ex.spec.version, 1); // 旧规约保留
  assert.deepEqual(ex.getConfig(), before); // 旧状态保留
  assert.equal(ex.hash, beforeHash);
});

test("migration violating new invariants is rejected", () => {
  const ex = new Executor(editorSpecV1());
  const result = ex.hotSwap(editorSpecV2(), (cfg) => ({
    path: cfg.path,
    data: { ...cfg.data, lines: 99, readonly: false },
  }));
  assert.equal(result.ok, false);
  assert.ok(result.errors.some((e) => e.includes("lines-bounded")));
  assert.equal(ex.spec.version, 1);
});

test("throwing migration is rejected and state preserved", () => {
  const ex = new Executor(editorSpecV1());
  const before = ex.getConfig();
  const result = ex.hotSwap(editorSpecV2(), () => {
    throw new Error("boom");
  });
  assert.equal(result.ok, false);
  assert.match(result.errors[0]!, /boom/);
  assert.deepEqual(ex.getConfig(), before);
});
