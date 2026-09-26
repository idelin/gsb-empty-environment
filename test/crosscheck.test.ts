import assert from "node:assert/strict";
import test from "node:test";
import { crossValidate } from "../src/crosscheck.ts";
import { editorSpecV1 } from "../src/editorSpec.ts";
import type { Ev } from "../src/spec.ts";

const mixed: Ev[] = [
  { type: "focus" },
  { type: "type", payload: { ch: "a" } },
  { type: "type", payload: { ch: "b" } },
  { type: "type", payload: { ch: "c" } },
  { type: "type", payload: { ch: "d" } }, // guard 拒绝
  { type: "save" },
  { type: "blur" },
  { type: "focus" },
  { type: "lock" },
  { type: "focus" }, // locked 下无此迁移
  { type: "unlock" },
  { type: "reset" },
];

test("executor trace matches checker one-step semantics", () => {
  const report = crossValidate(editorSpecV1(), mixed);
  assert.equal(report.steps, 9); // 9 步应用，3 步拒绝
  assert.deepEqual(report.mismatches, []);
});

test("cross-validation is deterministic", () => {
  const a = crossValidate(editorSpecV1(), mixed);
  const b = crossValidate(editorSpecV1(), mixed);
  assert.deepEqual(a, b);
});
